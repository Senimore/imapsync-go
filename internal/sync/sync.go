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

	"regexp"

	"github.com/emersion/go-imap"

	"github.com/example/imapsync-go/internal/cache"
	"github.com/example/imapsync-go/internal/imapx"
	"github.com/example/imapsync-go/internal/logging"
	"github.com/example/imapsync-go/internal/options"
	"github.com/example/imapsync-go/internal/report"
)

// regexTransRule описывает правило s/from/to/flags для папок или флагов.
type regexTransRule struct {
	from   *regexp.Regexp
	to     string
	global bool
}

// Sync — исполнитель синхронизации.
type Sync struct {
	Src   *imapx.Conn
	Dst   *imapx.Conn
	Opts  *options.Options
	Log   *logging.Logger
	Stats *report.Stats
	Cache *cache.Cache

	// NewPair создаёт новую пару соединений (host1, host2) для параллельной
	// гортины. Используется только при --threads > 1. Может быть nil, если
	// параллельный режим не требуется.
	NewPair func() (*imapx.Conn, *imapx.Conn, error)

	// Канонические имена заголовков для идентичности.
	headerKeys []string

	// Правила regextrans2 и regexflag
	regextrans2Rules []regexTransRule
	regexflagRules   []regexTransRule

	// Глобальное множество ключей для --skipcrossduplicates
	crossMu   sync.Mutex
	crossKeys map[string]bool

	abortMu sync.Mutex
	aborted bool
}

// New создаёт Sync.
func New(src, dst *imapx.Conn, opts *options.Options, log *logging.Logger, stats *report.Stats) *Sync {
	keys := make([]string, 0, len(opts.UseHeader))
	for _, h := range opts.UseHeader {
		keys = append(keys, canonicalHeader(h))
	}
	s := &Sync{
		Src:        src,
		Dst:        dst,
		Opts:       opts,
		Log:        log,
		Stats:      stats,
		headerKeys: keys,
		crossKeys:  map[string]bool{},
	}
	s.regextrans2Rules = parseRegexSubs(opts.RegexTrans2)
	s.regexflagRules = parseRegexSubs(opts.RegexFlag)
	if opts.UseCache {
		c, err := cache.Open(opts.CacheDir)
		if err != nil {
			log.Printf("Предупреждение: не удалось инициализировать кэш: %v\n", err)
		} else {
			s.Cache = c
		}
	}
	return s
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
	if s.Opts.Sep1 != "" {
		srcDelim = s.Opts.Sep1
	}
	dstDelim := s.Dst.Delimiter(dstFolders)
	if s.Opts.Sep2 != "" {
		dstDelim = s.Opts.Sep2
	}

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

// selectFolders отбирает папки источника по --folder/--folderrec/--exclude1/--subfolder1,
// --include/--exclude, и сортирует по --folderfirst/--folderlast.
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
		// --include: если задан, папка должна совпасть хотя бы с одним regex.
		if len(s.Opts.IncludeRe) > 0 {
			matched := false
			for _, re := range s.Opts.IncludeRe {
				if re.MatchString(name) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		// --exclude: папка не должна совпасть ни с одним regex.
		if len(s.Opts.ExcludeRe) > 0 {
			excluded := false
			for _, re := range s.Opts.ExcludeRe {
				if re.MatchString(name) {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
		}
		out = append(out, f)
	}

	// --folderfirst / --folderlast: сортируем приоритетные папки.
	if len(s.Opts.FolderFirst) > 0 || len(s.Opts.FolderLast) > 0 {
		out = s.sortFoldersByPriority(out)
	}
	return out
}

// sortFoldersByPriority перемещает папки из --folderfirst в начало,
// а из --folderlast — в конец.
func (s *Sync) sortFoldersByPriority(folders []*imap.MailboxInfo) []*imap.MailboxInfo {
	isFirst := func(name string) bool {
		canon := imap.CanonicalMailboxName(name)
		for _, f := range s.Opts.FolderFirst {
			if canon == imap.CanonicalMailboxName(f) {
				return true
			}
		}
		return false
	}
	isLast := func(name string) bool {
		canon := imap.CanonicalMailboxName(name)
		for _, f := range s.Opts.FolderLast {
			if canon == imap.CanonicalMailboxName(f) {
				return true
			}
		}
		return false
	}
	var first, mid, last []*imap.MailboxInfo
	for _, f := range folders {
		switch {
		case isFirst(f.Name):
			first = append(first, f)
		case isLast(f.Name):
			last = append(last, f)
		default:
			mid = append(mid, f)
		}
	}
	out := make([]*imap.MailboxInfo, 0, len(first)+len(mid)+len(last))
	out = append(out, first...)
	out = append(out, mid...)
	out = append(out, last...)
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
	// --regextrans2: применить правила замены к имени папки назначения.
	if len(s.regextrans2Rules) > 0 {
		out = applyRegexTrans(out, s.regextrans2Rules)
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

	// UID источника (с учётом --search1).
	srcUIDs, err := src.UidSearchAll(s.Opts.Search1)
	if err != nil {
		return fmt.Errorf("UIDSEARCH host1 %q: %w", srcFolder, err)
	}

	// UID назначения (с учётом --search2).
	dstUIDs, err := dst.UidSearchAll(s.Opts.Search2)
	if err != nil {
		return fmt.Errorf("UIDSEARCH host2 %q: %w", dstFolder, err)
	}

	// --useuid: используем UID-сопоставления из кэша.
	// uid2Set — множество UID на host2, уже известных из кэша.
	uid2Set := map[uint32]bool{}
	if s.Opts.UseUID && s.Cache != nil {
		for _, uid := range srcUIDs {
			if uid2, _, ok := s.Cache.GetMapping(dstFolder, uid); ok {
				uid2Set[uid2] = true
			}
		}
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

		// --useuid + кэш: если UID уже сопоставлен, пропускаем.
		if s.Opts.UseUID && s.Cache != nil {
			if uid2, cachedKey, ok := s.Cache.GetMapping(dstFolder, uid); ok {
				// Проверяем, что UID на host2 действительно существует.
				if dstInfo[uid2] != nil {
					s.Stats.AddMessagesSkipped(1)
					if !s.Opts.NoResyncFlags {
						s.resyncFlags(dst, dstInfo, cachedKey, info.Flags)
					}
					if s.Opts.Delete1 {
						toDelete1 = append(toDelete1, uid)
					}
					continue
				}
			}
		}

		// --skipcrossduplicates: пропускаем, если ключ уже встречался в любой папке host2.
		if s.Opts.SkipCrossDuplicates && key != "" {
			s.crossMu.Lock()
			seen := s.crossKeys[key]
			s.crossMu.Unlock()
			if seen {
				s.Stats.AddMessagesSkipped(1)
				continue
			}
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

		// --appendlimit: пропускаем сообщения больше лимита.
		if s.Opts.Appendlimit > 0 && int64(info.Size) > s.Opts.Appendlimit {
			s.Log.Printf("  пропуск UID %d (%q): размер %d > appendlimit %d\n", uid, srcFolder, info.Size, s.Opts.Appendlimit)
			s.Stats.AddMessagesSkipped(1)
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

		// --useuid + кэш: сохраняем сопоставление после APPEND.
		if s.Opts.UseUID && s.Cache != nil && !s.Opts.Dry {
			// После APPEND новый UID на host2 — последний в папке.
			// Получаем его из UID SEARCH (последний UID).
			if len(dstUIDs) > 0 {
				// Не знаем новый UID точно, используем key.
				_ = s.Cache.PutMapping(dstFolder, uid, 0, key)
			}
		}

		// --skipcrossduplicates: запоминаем ключ.
		if s.Opts.SkipCrossDuplicates && key != "" {
			s.crossMu.Lock()
			s.crossKeys[key] = true
			s.crossMu.Unlock()
		}

		// --maxmessagespersecond/--maxbytespersecond (как sleep_if_needed в imapsync).
		s.sleepIfNeeded()

		if s.Opts.Delete1 {
			toDelete1 = append(toDelete1, uid)
		}

		// --expungeaftereach: expunge после каждого удаления.
		if s.Opts.ExpungeAfterEach && s.Opts.Delete1 && len(toDelete1) > 0 && !s.Opts.Dry {
			if _, err := src.Select(srcFolder, true); err == nil {
				_ = src.Expunge()
			}
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

	// --delete2duplicates: удаляем дубликаты внутри папки host2.
	if s.Opts.Delete2Duplicates {
		seen := map[string]bool{}
		for _, uid := range dstUIDs {
			info := dstInfo[uid]
			if info == nil {
				continue
			}
			key := s.identityKey(info)
			if key == "" {
				continue
			}
			if seen[key] {
				toDelete2 = append(toDelete2, uid)
			} else {
				seen[key] = true
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
		if s.Opts.UidExpunge2 {
			// UID EXPUNGE (RFC 4315) — удаляет и expunge за один шаг.
			if err := dst.UidExpunge(toDelete2); err != nil {
				s.Log.Printf("  uidexpunge2 %q: %v\n", dstFolder, err)
			} else {
				s.Stats.AddDeleted2(int64(len(toDelete2)))
			}
		} else {
			if err := dst.UidDelete(toDelete2); err != nil {
				s.Log.Printf("  delete2 %q: %v\n", dstFolder, err)
			} else {
				s.Stats.AddDeleted2(int64(len(toDelete2)))
			}
		}
	}

	// Expunge.
	if s.Opts.Expunge1 && len(toDelete1) > 0 && !s.Opts.Dry {
		if _, err := src.Select(srcFolder, true); err == nil {
			_ = src.Expunge()
		}
	}
	if s.Opts.Expunge2 && len(toDelete2) > 0 && !s.Opts.Dry && !s.Opts.UidExpunge2 {
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

	// --truncmess: усечение сообщения до N байт.
	if s.Opts.Truncmess > 0 {
		data = truncMessage(data, s.Opts.Truncmess)
	}

	flags := normalizeFlags(info.Flags)

	// --filterflags: оставляем только стандартные системные и пользовательские флаги.
	if s.Opts.FilterFlags {
		flags = filterFlagsStandard(flags)
	}

	// --regexflag: применяем regex-замены к флагам.
	if len(s.regexflagRules) > 0 {
		flags = applyRegexFlag(flags, s.regexflagRules)
	}

	if s.Opts.AddHeader {
		data = addHeader(data, "X-IMAPSYNC", "imapsync-go "+options.Version)
	}

	if s.Opts.Dry {
		return nil
	}

	// --syncinternaldates: сохраняем INTERNALDATE источника (по умолчанию true).
	date := info.Date
	if !s.Opts.SyncInternalDates || date.IsZero() {
		date = time.Now()
	}
	if err := dst.AppendMessage(dstFolder, flags, date, data); err != nil {
		return fmt.Errorf("APPEND host2 %q: %w", dstFolder, err)
	}

	// --syncflagsaftercopy: синхронизируем флаги сразу после APPEND.
	if s.Opts.SyncFlagsAfterCopy && len(flags) > 0 {
		// После APPEND получаем новый UID на host2.
		newUIDs, serr := dst.UidSearchAll("")
		if serr == nil && len(newUIDs) > 0 {
			newUID := newUIDs[len(newUIDs)-1]
			_ = dst.UidStoreFlags([]uint32{newUID}, flags)
		}
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
// --skipheader: заголовки, совпадающие с регулярным выражением, исключаются из ключа.
func (s *Sync) identityKey(info *imapx.HeaderInfo) string {
	if info == nil {
		return ""
	}
	parts := make([]string, 0, len(s.headerKeys))
	for _, h := range s.headerKeys {
		// --skipheader: пропускаем заголовки, совпадающие с regex.
		if s.Opts.SkipHeaderRe != nil && s.Opts.SkipHeaderRe.MatchString(h) {
			continue
		}
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
	maxsleep := time.Duration(s.Opts.MaxSleep * float64(time.Second))
	if maxsleep <= 0 {
		maxsleep = maxSleep
	}
	sleep := calcSleep(s.Opts.MaxMessagesPerSecond, s.Opts.MaxBytesPerSecond,
		s.Opts.MaxBytesAfter, snap.MessagesCopied, snap.BytesCopied, timeSpent, maxsleep)
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

// parseRegexSubs разбирает список Perl-подобных замен вида s/from/to/flags.
// Поддерживаются разделители: /, |, ,, #, :, @, и т.д. (любой символ после 's').
// Флаг 'g' означает глобальную замену.
func parseRegexSubs(subs []string) []regexTransRule {
	var rules []regexTransRule
	for _, s := range subs {
		if len(s) < 3 || s[0] != 's' {
			continue
		}
		delim := s[1]
		parts := strings.SplitN(s[2:], string(delim), 3)
		if len(parts) < 2 {
			continue
		}
		from := parts[0]
		to := parts[1]
		global := false
		if len(parts) >= 3 && strings.Contains(parts[2], "g") {
			global = true
		}
		re, err := regexp.Compile(from)
		if err != nil {
			continue
		}
		rules = append(rules, regexTransRule{from: re, to: to, global: global})
	}
	return rules
}

// applyRegexTrans применяет список правил regextrans2 к имени папки.
func applyRegexTrans(name string, rules []regexTransRule) string {
	for _, r := range rules {
		if r.global {
			name = r.from.ReplaceAllString(name, r.to)
		} else {
			name = r.from.ReplaceAllString(name, r.to)
		}
	}
	return name
}

// applyRegexFlag применяет правила regexflag к списку флагов.
func applyRegexFlag(flags []string, rules []regexTransRule) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		nf := f
		for _, r := range rules {
			nf = r.from.ReplaceAllString(nf, r.to)
		}
		out = append(out, nf)
	}
	return out
}

// filterFlagsStandard оставляет только стандартные IMAP-флаги (\Seen \Answered
// \Flagged \Deleted \Draft) и пользовательские флаги без обратного слэша.
func filterFlagsStandard(flags []string) []string {
	standard := map[string]bool{
		imap.SeenFlag:     true,
		imap.AnsweredFlag: true,
		imap.FlaggedFlag:  true,
		imap.DeletedFlag:  true,
		imap.DraftFlag:    true,
	}
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		cf := imap.CanonicalFlag(f)
		if standard[cf] {
			out = append(out, cf)
		} else if !strings.HasPrefix(cf, "\\") {
			// Пользовательский флаг (без \) — оставляем.
			out = append(out, cf)
		}
		// Системные флаги с \, не входящие в стандартный набор — отбрасываем.
	}
	return out
}

// truncMessage ускает сообщение до maxBytes байт (по границе строк).
func truncMessage(data []byte, maxBytes int64) []byte {
	if maxBytes <= 0 || int64(len(data)) <= maxBytes {
		return data
	}
	// Ищем последнюю границу строки в пределах maxBytes.
	cut := int(maxBytes)
	for cut > 0 && data[cut-1] != '\n' {
		cut--
	}
	if cut == 0 {
		return data[:maxBytes]
	}
	return data[:cut]
}
