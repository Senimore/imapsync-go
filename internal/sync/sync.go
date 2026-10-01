// Package sync реализует основную логику синхронизации двух IMAP-аккаунтов:
// рекурсивный обход папок источника, маппинг на назначение, определение
// дубликатов по заголовкам, перенос сообщений с флагами и опции delete1/delete2.
//
// Важно: у одного IMAP-соединения может быть открыта только одна папка
// (SELECT). Поэтому при --threads > 1 каждая горутина получает собственную
// пару соединений через NewPair (как в imapsync, где параллелизм — это
// отдельные процессы с отдельными соединениями).
package sync

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"

	"github.com/example/imapsync-go/internal/imapx"
	"github.com/example/imapsync-go/internal/logging"
	"github.com/example/imapsync-go/internal/options"
	"github.com/example/imapsync-go/internal/report"
)

// Sync — исполнитель синхронизации.
type Sync struct {
	Src   *imapx.Conn
	Dst   *imapx.Conn
	Opts  *options.Options
	Log   *logging.Logger
	Stats *report.Stats

	// NewPair создаёт новую пару соединений (host1, host2) для параллельной
	// гортины. Используется только при --threads > 1. Может быть nil, если
	// параллельный режим не требуется.
	NewPair func() (*imapx.Conn, *imapx.Conn, error)

	// Канонические имена заголовков для идентичности.
	headerKeys []string

	abortMu sync.Mutex
	aborted bool
}

// New создаёт Sync.
func New(src, dst *imapx.Conn, opts *options.Options, log *logging.Logger, stats *report.Stats) *Sync {
	keys := make([]string, 0, len(opts.UseHeader))
	for _, h := range opts.UseHeader {
		keys = append(keys, canonicalHeader(h))
	}
	return &Sync{Src: src, Dst: dst, Opts: opts, Log: log, Stats: stats, headerKeys: keys}
}

// Abort помечает синхронизацию прерванной (по сигналу).
func (s *Sync) Abort() {
	s.abortMu.Lock()
	s.aborted = true
	s.abortMu.Unlock()
}

func (s *Sync) isAborted() bool {
	s.abortMu.Lock()
	defer s.abortMu.Unlock()
	return s.aborted
}

// canonicalHeader приводит имя заголовка к каноническому виду
// (совместимому с net/http и с ParseHeaderValues в imapx).
func canonicalHeader(h string) string {
	if h == "" {
		return h
	}
	return http.CanonicalHeaderKey(h)
}

// Run выполняет полную синхронизацию.
func (s *Sync) Run() error {
	srcFolders, err := s.Src.ListFolders()
	if err != nil {
		return fmt.Errorf("LIST host1: %w", err)
	}
	dstFolders, err := s.Dst.ListFolders()
	if err != nil {
		return fmt.Errorf("LIST host2: %w", err)
	}

	srcDelim := s.Src.Delimiter(srcFolders)
	dstDelim := s.Dst.Delimiter(dstFolders)

	// Отображения f1f2.
	f1f2 := map[string]string{}
	for _, p := range s.Opts.F1F2 {
		f1f2[imap.CanonicalMailboxName(p[0])] = p[1]
	}

	// Отбираем папки источника.
	selected := s.selectFolders(srcFolders, srcDelim)

	// Подсчёт суммарных размеров host1/host2 (по STATUS MESSAGES).
	s.computeSizes(s.Src, s.Dst, srcFolders, dstFolders)

	threads := s.Opts.Threads
	if threads < 1 {
		threads = 1
	}

	var runErr error
	var errMu sync.Mutex

	if threads == 1 {
		// Последовательно на основном соединении.
		for _, sf := range selected {
			if s.isAborted() {
				break
			}
			dstName := s.mapFolder(sf.Name, srcDelim, dstDelim, f1f2)
			if err := s.syncFolder(s.Src, s.Dst, sf.Name, dstName); err != nil {
				runErr = firstErr(runErr, err)
				s.Stats.AddErrors(1)
				s.Log.Printf("ОШИБКА синхронизации %q -> %q: %v\n", sf.Name, dstName, err)
				if s.checkErrorsMax() {
					break
				}
			}
		}
	} else {
		// Параллельно: по паре соединений на горутину.
		if s.NewPair == nil {
			return fmt.Errorf("параллельный режим (--threads>1) требует NewPair")
		}
		sem := make(chan struct{}, threads)
		var wg sync.WaitGroup
		for _, sf := range selected {
			if s.isAborted() {
				break
			}
			dstName := s.mapFolder(sf.Name, srcDelim, dstDelim, f1f2)
			wg.Add(1)
			sem <- struct{}{}
			go func(srcFolder, dstFolder string) {
				defer wg.Done()
				defer func() { <-sem }()
				if s.isAborted() {
					return
				}
				src, dst, cerr := s.NewPair()
				if cerr != nil {
					errMu.Lock()
					runErr = firstErr(runErr, cerr)
					errMu.Unlock()
					s.Stats.AddErrors(1)
					s.Log.Printf("ОШИБКА подключения для %q: %v\n", srcFolder, cerr)
					s.checkErrorsMax()
					return
				}
				defer src.Logout()
				defer dst.Logout()
				if err := s.syncFolder(src, dst, srcFolder, dstFolder); err != nil {
					errMu.Lock()
					runErr = firstErr(runErr, err)
					errMu.Unlock()
					s.Stats.AddErrors(1)
					s.Log.Printf("ОШИБКА синхронизации %q -> %q: %v\n", srcFolder, dstFolder, err)
					s.checkErrorsMax()
				}
			}(sf.Name, dstName)
		}
		wg.Wait()
	}

	// delete2folders: удалить папки назначения, отсутствующие в источнике.
	if s.Opts.Delete2Folders {
		if err := s.deleteExtraFolders(s.Src, s.Dst, srcFolders, dstFolders); err != nil {
			s.Log.Printf("ОШИБКА delete2folders: %v\n", err)
		}
	}

	return runErr
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// selectFolders отбирает папки источника по --folder/--folderrec/--exclude1/--subfolder1.
// Папки с флагом \Noselect (неселектабельные, special-use) пропускаются,
// как это делает imapsync с --checkselectable.
func (s *Sync) selectFolders(folders []*imap.MailboxInfo, delim string) []*imap.MailboxInfo {
	var out []*imap.MailboxInfo
	for _, f := range folders {
		name := f.Name
		if hasAttr(f.Attributes, imap.NoSelectAttr) {
			s.Log.Debugf("папка %q помечена \\Noselect — пропускается\n", name)
			continue
		}
		if imap.CanonicalMailboxName(name) == imap.CanonicalMailboxName(imap.InboxName) {
			// INBOX всегда включаем (если нет явного списка folder)
			if len(s.Opts.Folder) == 0 && len(s.Opts.FolderRec) == 0 {
				out = append(out, f)
				continue
			}
		}
		if !s.folderSelected(name, delim) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// hasAttr проверяет наличие атрибута папки (без учёта регистра флага).
func hasAttr(attrs []string, want string) bool {
	for _, a := range attrs {
		if strings.EqualFold(a, want) {
			return true
		}
	}
	return false
}

func (s *Sync) folderSelected(name, delim string) bool {
	canon := imap.CanonicalMailboxName(name)

	// subfolder1: ограничить префиксом.
	if s.Opts.Subfolder1 != "" {
		if !strings.HasPrefix(canon, imap.CanonicalMailboxName(s.Opts.Subfolder1)) {
			return false
		}
	}

	// exclude1 / exclude-all по подстроке.
	for _, ex := range s.Opts.Exclude1 {
		if strings.Contains(canon, imap.CanonicalMailboxName(ex)) {
			return false
		}
	}
	for _, ex := range s.Opts.ExcludeAll {
		if strings.Contains(canon, imap.CanonicalMailboxName(ex)) {
			return false
		}
	}

	// Явные списки folder / folderrec.
	if len(s.Opts.Folder) > 0 || len(s.Opts.FolderRec) > 0 {
		for _, f := range s.Opts.Folder {
			if canon == imap.CanonicalMailboxName(f) {
				return true
			}
		}
		for _, fr := range s.Opts.FolderRec {
			prefix := imap.CanonicalMailboxName(fr)
			if canon == prefix || strings.HasPrefix(canon, prefix+strings.ToUpper(delim)) {
				return true
			}
		}
		return false
	}

	return true
}

// mapFolder вычисляет имя папки назначения.
//
// Соответствует prefix_seperator_invertion() из imapsync:
//  1. удалить --prefix1 из начала имени папки источника;
//  2. заменить разделитель источника на разделитель назначения;
//  3. добавить --prefix2 ко всем папкам host2, кроме INBOX.
func (s *Sync) mapFolder(srcName, srcDelim, dstDelim string, f1f2 map[string]string) string {
	canon := imap.CanonicalMailboxName(srcName)
	if mapped, ok := f1f2[canon]; ok {
		return mapped
	}
	name := srcName
	// --prefix1: удалить префикс (обычно "INBOX." или "INBOX/").
	if s.Opts.Prefix1 != "" {
		name = strings.TrimPrefix(name, s.Opts.Prefix1)
	}
	// subfolder1 -> subfolder2
	if s.Opts.Subfolder1 != "" && s.Opts.Subfolder2 != "" {
		prefix := imap.CanonicalMailboxName(s.Opts.Subfolder1)
		if strings.HasPrefix(canon, prefix) {
			rest := canon[len(prefix):]
			return s.Opts.Subfolder2 + rest
		}
	}
	// automap / по умолчанию: то же имя (с учётом разделителя назначения).
	out := convertDelim(name, srcDelim, dstDelim)
	// --prefix2: добавить префикс ко всем папкам host2, кроме INBOX.
	if s.Opts.Prefix2 != "" && imap.CanonicalMailboxName(out) != imap.CanonicalMailboxName(imap.InboxName) {
		out = s.Opts.Prefix2 + out
	}
	return out
}

// convertDelim меняет разделитель пути папки.
func convertDelim(name, from, to string) string {
	if from == "" || from == to {
		return name
	}
	return strings.ReplaceAll(name, from, to)
}

// syncFolder синхронизирует одну пару папок на заданных соединениях.
func (s *Sync) syncFolder(src, dst *imapx.Conn, srcFolder, dstFolder string) error {
	s.Log.Printf("Синхронизация папки %q -> %q\n", srcFolder, dstFolder)

	// Открываем источник (read-only).
	srcStatus, err := src.Select(srcFolder, false)
	if err != nil {
		return err
	}

	// --skipemptyfolders (по умолчанию): пустые папки host1 не синхронизируются
	// и не создаются на host2, как это делает imapsync.
	if s.Opts.SkipEmptyFolders && srcStatus.Messages == 0 {
		s.Log.Printf("  пропуск пустой папки host1 %q (0 сообщений)\n", srcFolder)
		return nil
	}

	// Убедимся, что папка назначения существует.
	if err := s.ensureDstFolder(dst, dstFolder); err != nil {
		return err
	}

	// Открываем назначение (read-write). В dry-режиме папка не создаётся,
	// поэтому её отсутствие — не ошибка, а информация.
	if _, err := dst.Select(dstFolder, true); err != nil {
		if s.Opts.Dry {
			s.Log.Printf("  [dry] папка назначения %q отсутствует (будет создана)\n", dstFolder)
			return nil
		}
		return err
	}

	// UID источника.
	srcUIDs, err := src.UidSearchAll()
	if err != nil {
		return fmt.Errorf("UIDSEARCH host1 %q: %w", srcFolder, err)
	}

	// UID назначения.
	dstUIDs, err := dst.UidSearchAll()
	if err != nil {
		return fmt.Errorf("UIDSEARCH host2 %q: %w", dstFolder, err)
	}

	// Заголовки источника.
	srcInfo, err := src.FetchHeaderMap(srcUIDs, s.Opts.UseHeader)
	if err != nil {
		return fmt.Errorf("FETCH headers host1 %q: %w", srcFolder, err)
	}
	// Заголовки назначения (для определения дубликатов).
	dstInfo, err := dst.FetchHeaderMap(dstUIDs, s.Opts.UseHeader)
	if err != nil {
		return fmt.Errorf("FETCH headers host2 %q: %w", dstFolder, err)
	}

	// Множество ключей, присутствующих на host2.
	dstKeys := map[string]bool{}
	for _, info := range dstInfo {
		if k := s.identityKey(info); k != "" {
			dstKeys[k] = true
		}
	}

	// Ключи источника (для delete2).
	srcKeySet := map[string]bool{}

	var toDelete1 []uint32
	var toDelete2 []uint32

	for _, uid := range srcUIDs {
		if s.isAborted() {
			break
		}
		info := srcInfo[uid]
		if info == nil {
			continue
		}
		if !s.passFilters(info) {
			continue
		}
		key := s.identityKey(info)
		if key == "" {
			s.Stats.AddUnidentified(1)
		} else {
			srcKeySet[key] = true
		}

		// Дубликат на host2?
		if key != "" && dstKeys[key] {
			s.Stats.AddMessagesSkipped(1)
			// Пересинхронизация флагов.
			if !s.Opts.NoResyncFlags {
				s.resyncFlags(dst, dstInfo, key, info.Flags)
			}
			if s.Opts.Delete1 {
				toDelete1 = append(toDelete1, uid)
			}
			continue
		}

		// Переносим сообщение.
		if err := s.copyMessage(src, dst, dstFolder, uid, info); err != nil {
			if errors.Is(err, errSkipMess) {
				s.Stats.AddMessagesSkippedRegex(1)
				s.Log.Printf("  пропуск UID %d (%q): совпадение с --skipmess\n", uid, srcFolder)
				continue
			}
			s.Log.Printf("  пропуск UID %d (%q): %v\n", uid, srcFolder, err)
			s.Stats.AddErrors(1)
			s.checkErrorsMax()
			continue
		}
		s.Stats.AddMessagesCopied(1)
		s.Stats.AddBytes(int64(info.Size))

		// --maxmessagespersecond/--maxbytespersecond (как sleep_if_needed в imapsync).
		s.sleepIfNeeded()

		if s.Opts.Delete1 {
			toDelete1 = append(toDelete1, uid)
		}

		if s.Opts.ExitWhenOver > 0 && s.Stats.Get().BytesCopied >= s.Opts.ExitWhenOver {
			s.Log.Printf("Достигнут лимит --exitwhenover %d байт, останавливаемся\n", s.Opts.ExitWhenOver)
			s.Abort()
			break
		}
	}

	// delete2: сообщения на host2, ключей которых нет в источнике.
	if s.Opts.Delete2 {
		for _, uid := range dstUIDs {
			info := dstInfo[uid]
			if info == nil {
				continue
			}
			key := s.identityKey(info)
			if key == "" {
				// Неидентифицированное сообщение на host2 — не удаляем.
				continue
			}
			if !srcKeySet[key] {
				toDelete2 = append(toDelete2, uid)
			}
		}
	}

	// Применяем удаления.
	if len(toDelete1) > 0 && !s.Opts.Dry {
		if err := src.UidDelete(toDelete1); err != nil {
			s.Log.Printf("  delete1 %q: %v\n", srcFolder, err)
		} else {
			s.Stats.AddDeleted1(int64(len(toDelete1)))
		}
	}
	if len(toDelete2) > 0 && !s.Opts.Dry {
		if err := dst.UidDelete(toDelete2); err != nil {
			s.Log.Printf("  delete2 %q: %v\n", dstFolder, err)
		} else {
			s.Stats.AddDeleted2(int64(len(toDelete2)))
		}
	}

	// Expunge.
	if s.Opts.Expunge1 && len(toDelete1) > 0 && !s.Opts.Dry {
		if _, err := src.Select(srcFolder, true); err == nil {
			_ = src.Expunge()
		}
	}
	if s.Opts.Expunge2 && len(toDelete2) > 0 && !s.Opts.Dry {
		if _, err := dst.Select(dstFolder, true); err == nil {
			_ = dst.Expunge()
		}
	}

	s.Stats.AddFoldersSynced(1)

	snap := s.Stats.Get()
	s.Log.Printf("  %q -> %q: всего на host1 %d UID (всего скопировано %d, дубликатов %d)\n",
		srcFolder, dstFolder, len(srcUIDs), snap.MessagesCopied, snap.MessagesSkipped)
	return nil
}

// ensureDstFolder создаёт папку назначения при необходимости.
func (s *Sync) ensureDstFolder(dst *imapx.Conn, dstFolder string) error {
	if imap.CanonicalMailboxName(dstFolder) == imap.CanonicalMailboxName(imap.InboxName) {
		return nil // INBOX существует всегда
	}
	if s.Opts.Dry {
		return nil
	}
	if err := dst.Create(dstFolder); err != nil {
		// Уже существует? Проверяем по STATUS.
		if _, serr := dst.Status(dstFolder); serr == nil {
			return nil
		}
		return fmt.Errorf("CREATE host2 %q: %w", dstFolder, err)
	}
	if s.Opts.Subscribe2 {
		_ = dst.Subscribe(dstFolder)
	}
	s.Stats.AddFoldersCreated(1)
	return nil
}

// errSkipMess — сообщение пропущено по --skipmess (не является ошибкой).
var errSkipMess = errors.New("skipmess")

// copyMessage переносит одно сообщение с host1 на host2.
func (s *Sync) copyMessage(src, dst *imapx.Conn, dstFolder string, uid uint32, info *imapx.HeaderInfo) error {
	data, _, err := src.FetchRfc822(uid)
	if err != nil {
		return err
	}

	// --skipmess: пропускаем сообщения, полное содержимое которых
	// совпадает с любым из заданных регулярных выражений (как в imapsync).
	if s.matchSkipMess(data) {
		return errSkipMess
	}

	flags := normalizeFlags(info.Flags)

	if s.Opts.AddHeader {
		data = addHeader(data, "X-IMAPSYNC", "imapsync-go "+options.Version)
	}

	if s.Opts.Dry {
		return nil
	}

	date := info.Date
	if date.IsZero() {
		date = time.Now()
	}
	if err := dst.AppendMessage(dstFolder, flags, date, data); err != nil {
		return fmt.Errorf("APPEND host2 %q: %w", dstFolder, err)
	}
	return nil
}

// resyncFlags находит на host2 сообщение с данным ключом и выставляет флаги как на host1.
func (s *Sync) resyncFlags(dst *imapx.Conn, dstInfo map[uint32]*imapx.HeaderInfo, key string, flags []string) {
	for uid, info := range dstInfo {
		if s.identityKey(info) == key {
			target := normalizeFlags(flags)
			if !sameFlags(info.Flags, target) {
				if !s.Opts.Dry {
					if err := dst.UidStoreFlags([]uint32{uid}, target); err == nil {
						s.Stats.AddMessagesFlagged(1)
					}
				}
			}
			return
		}
	}
}

// identityKey строит ключ идентичности из заголовков. Пустая строка — неидентифицировано.
func (s *Sync) identityKey(info *imapx.HeaderInfo) string {
	if info == nil {
		return ""
	}
	parts := make([]string, 0, len(s.headerKeys))
	for _, h := range s.headerKeys {
		vals := info.Values[h]
		if len(vals) == 0 {
			continue
		}
		parts = append(parts, h+":"+strings.Join(vals, ","))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "|")
}

// matchSkipMess проверяет содержимое сообщения по --skipmess.
func (s *Sync) matchSkipMess(data []byte) bool {
	for _, re := range s.Opts.SkipMessRe {
		if re.Match(data) {
			return true
		}
	}
	return false
}

// maxSleep — верхняя граница паузы троттлинга ($MAX_SLEEP в imapsync = 2 с).
const maxSleep = 2 * time.Second

// calcSleep вычисляет длительность паузы троттлинга в секундах
// (sleep_if_needed в imapsync):
//
//	sleep = min(maxsleep, max(nbMsgTransferred/maxMsgPerSec,
//	(max(totalBytes - maxbytesafter, 0))/maxBytesPerSec) - времяСНачала)
func calcSleep(maxMsgPerSec float64, maxBytesPerSec int64, maxBytesAfter int64,
	nbMsgs int64, totalBytes int64, timeSpent float64, maxsleep time.Duration) float64 {
	sleepMsgs := 0.0
	if maxMsgPerSec > 0 {
		sleepMsgs = float64(nbMsgs)/maxMsgPerSec - timeSpent
		if sleepMsgs < 0 {
			sleepMsgs = 0
		}
	}
	sleepBytes := 0.0
	if maxBytesPerSec > 0 {
		consider := float64(totalBytes) - float64(maxBytesAfter)
		if consider < 0 {
			consider = 0
		}
		sleepBytes = consider/float64(maxBytesPerSec) - timeSpent
		if sleepBytes < 0 {
			sleepBytes = 0
		}
	}
	sleep := math.Max(sleepMsgs, sleepBytes)
	if sleep > maxsleep.Seconds() {
		sleep = maxsleep.Seconds()
	}
	return sleep
}

// sleepIfNeeded реализует --maxmessagespersecond/--maxbytespersecond.
func (s *Sync) sleepIfNeeded() {
	if s.Opts.MaxMessagesPerSecond <= 0 && s.Opts.MaxBytesPerSecond <= 0 {
		return
	}
	snap := s.Stats.Get()
	timeSpent := time.Since(s.Stats.StartTime).Seconds()
	sleep := calcSleep(s.Opts.MaxMessagesPerSecond, s.Opts.MaxBytesPerSecond,
		s.Opts.MaxBytesAfter, snap.MessagesCopied, snap.BytesCopied, timeSpent, maxSleep)
	if sleep > 0 {
		s.Log.Printf("  sleeping %.2f s\n", sleep)
		time.Sleep(time.Duration(sleep * float64(time.Second)))
	}
}

// checkErrorsMax останавливает синхронизацию при достижении --errorsmax
// (в imapsync: nb_errors >= errorsmax -> выход). Возвращает true, если
// лимит достигнут.
func (s *Sync) checkErrorsMax() bool {
	if s.Stats.Get().Errors < int64(s.Opts.ErrorsMax) {
		return false
	}
	s.Log.Printf("Maximum number of errors %d reached ( you can change %d to any value, for example 100 with --errorsmax 100 ). Exiting.\n",
		s.Opts.ErrorsMax, s.Opts.ErrorsMax)
	s.Abort()
	return true
}

// passFilters применяет minsize/maxsize/minage/maxage.
func (s *Sync) passFilters(info *imapx.HeaderInfo) bool {
	if s.Opts.MinSize > 0 && int64(info.Size) < s.Opts.MinSize {
		return false
	}
	if s.Opts.MaxSize > 0 && int64(info.Size) > s.Opts.MaxSize {
		return false
	}
	if s.Opts.MinAge > 0 || s.Opts.MaxAge > 0 {
		age := time.Since(info.Date).Hours() / 24.0
		if s.Opts.MinAge > 0 && age < s.Opts.MinAge {
			return false
		}
		if s.Opts.MaxAge > 0 && age > s.Opts.MaxAge {
			return false
		}
	}
	return true
}

// normalizeFlags канонизирует флаги для APPEND/STORE.
func normalizeFlags(flags []string) []string {
	out := make([]string, 0, len(flags))
	seen := map[string]bool{}
	for _, f := range flags {
		cf := imap.CanonicalFlag(f)
		if seen[cf] {
			continue
		}
		seen[cf] = true
		out = append(out, cf)
	}
	return out
}

func sameFlags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := map[string]bool{}
	for _, f := range a {
		sa[imap.CanonicalFlag(f)] = true
	}
	for _, f := range b {
		if !sa[imap.CanonicalFlag(f)] {
			return false
		}
	}
	return true
}

// addHeader вставляет заголовок в начало сообщения.
func addHeader(data []byte, name, value string) []byte {
	line := []byte(name + ": " + value + "\r\n")
	out := make([]byte, 0, len(data)+len(line))
	out = append(out, line...)
	out = append(out, data...)
	return out
}

// computeSizes подсчитывает суммарные размеры (по STATUS MESSAGES).
func (s *Sync) computeSizes(src, dst *imapx.Conn, srcFolders, dstFolders []*imap.MailboxInfo) {
	// STATUS не отдаёт точный размер в байтах, поэтому используем число
	// сообщений как прокси-метрику (как это делает imapsync для оценки).
	var h1, h2 int64
	for _, f := range srcFolders {
		if st, err := src.Status(f.Name); err == nil {
			h1 += int64(st.Messages)
		}
	}
	for _, f := range dstFolders {
		if st, err := dst.Status(f.Name); err == nil {
			h2 += int64(st.Messages)
		}
	}
	s.Stats.SetHost1(h1)
	s.Stats.SetHost2(h2)
}

// deleteExtraFolders удаляет папки назначения, отсутствующие в источнике.
func (s *Sync) deleteExtraFolders(src, dst *imapx.Conn, srcFolders, dstFolders []*imap.MailboxInfo) error {
	srcSet := map[string]bool{}
	for _, f := range srcFolders {
		srcSet[imap.CanonicalMailboxName(f.Name)] = true
	}
	for _, f := range dstFolders {
		canon := imap.CanonicalMailboxName(f.Name)
		if canon == imap.CanonicalMailboxName(imap.InboxName) {
			continue // INBOX не удаляем
		}
		if !srcSet[canon] {
			if s.Opts.Dry {
				s.Log.Printf("delete2folders: (dry) удалю %q\n", f.Name)
				continue
			}
			if err := dst.DeleteFolder(f.Name); err != nil {
				s.Log.Printf("delete2folders: не удалось удалить %q: %v\n", f.Name, err)
				continue
			}
			s.Stats.AddFoldersDeleted(1)
			s.Log.Printf("delete2folders: удалена %q\n", f.Name)
		}
	}
	return nil
}
