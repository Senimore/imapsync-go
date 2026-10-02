// Package report собирает статистику синхронизации и формирует итоговый
// отчёт, сопоставимый с выводами imapsync.
package report

import (
	"fmt"
	"sync"
	"time"
)

// Stats — потокобезопасные счётчики хода синхронизации.
type Stats struct {
	mu sync.Mutex

	FoldersSynced  int64
	FoldersCreated int64
	FoldersDeleted int64

	MessagesCopied       int64
	MessagesSkipped      int64 // дубликаты (уже есть на host2)
	MessagesSkippedRegex int64 // пропущено по --skipmess
	MessagesDeleted1     int64
	MessagesDeleted2     int64
	MessagesFlagged      int64
	MessagesUnidentified int64

	BytesCopied int64
	BytesHost1  int64
	BytesHost2  int64

	Errors int64

	StartTime time.Time
}

// New создаёт Stats с отметкой старта.
func New() *Stats {
	return &Stats{StartTime: time.Now()}
}

func (s *Stats) add(field *int64, n int64) {
	s.mu.Lock()
	*field += n
	s.mu.Unlock()
}

// Inc инкрементирует именованный счётчик.
func (s *Stats) Inc(field *int64) { s.add(field, 1) }

// Add прибавляет n к именованному счётчику (потокобезопасно).
func (s *Stats) Add(field *int64, n int64) { s.add(field, n) }

// Convenience-обёртки для счётчиков.
func (s *Stats) AddMessagesCopied(n int64)       { s.add(&s.MessagesCopied, n) }
func (s *Stats) AddMessagesSkipped(n int64)      { s.add(&s.MessagesSkipped, n) }
func (s *Stats) AddMessagesSkippedRegex(n int64) { s.add(&s.MessagesSkippedRegex, n) }
func (s *Stats) AddMessagesFlagged(n int64)      { s.add(&s.MessagesFlagged, n) }
func (s *Stats) AddUnidentified(n int64)         { s.add(&s.MessagesUnidentified, n) }
func (s *Stats) AddDeleted1(n int64)             { s.add(&s.MessagesDeleted1, n) }
func (s *Stats) AddDeleted2(n int64)             { s.add(&s.MessagesDeleted2, n) }
func (s *Stats) AddErrors(n int64)               { s.add(&s.Errors, n) }
func (s *Stats) AddFoldersSynced(n int64)        { s.add(&s.FoldersSynced, n) }
func (s *Stats) AddFoldersCreated(n int64)       { s.add(&s.FoldersCreated, n) }
func (s *Stats) AddFoldersDeleted(n int64)       { s.add(&s.FoldersDeleted, n) }

// AddBytes прибавляет перенесённые байты.
func (s *Stats) AddBytes(n int64) { s.add(&s.BytesCopied, n) }

// AddHost1/AddHost2 задают суммарные размеры (устанавливаются один раз).
func (s *Stats) SetHost1(n int64) { s.add(&s.BytesHost1, n) }
func (s *Stats) SetHost2(n int64) { s.add(&s.BytesHost2, n) }

// Snapshot возвращает копию счётчиков для безопасного чтения.
type Snapshot struct {
	FoldersSynced, FoldersCreated, FoldersDeleted int64
	MessagesCopied, MessagesSkipped               int64
	MessagesSkippedRegex                          int64
	MessagesDeleted1, MessagesDeleted2            int64
	MessagesFlagged, MessagesUnidentified         int64
	BytesCopied, BytesHost1, BytesHost2           int64
	Errors                                        int64
	Elapsed                                       time.Duration
}

// Get возвращает снимок текущих значений.
func (s *Stats) Get() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		FoldersSynced:        s.FoldersSynced,
		FoldersCreated:       s.FoldersCreated,
		FoldersDeleted:       s.FoldersDeleted,
		MessagesCopied:       s.MessagesCopied,
		MessagesSkipped:      s.MessagesSkipped,
		MessagesSkippedRegex: s.MessagesSkippedRegex,
		MessagesDeleted1:     s.MessagesDeleted1,
		MessagesDeleted2:     s.MessagesDeleted2,
		MessagesFlagged:      s.MessagesFlagged,
		MessagesUnidentified: s.MessagesUnidentified,
		BytesCopied:          s.BytesCopied,
		BytesHost1:           s.BytesHost1,
		BytesHost2:           s.BytesHost2,
		Errors:               s.Errors,
		Elapsed:              time.Since(s.StartTime),
	}
}

// Summary возвращает краткую сводку по одной папке (для построчного вывода).
func (s *Stats) Summary() string {
	snap := s.Get()
	return fmt.Sprintf(
		"folders: %d synced, %d created, %d deleted | "+
			"messages: %d copied, %d skipped(dup), %d skipped(regex), %d flagged, %d unidentified, "+
			"%d deleted1, %d deleted2 | bytes: %d | errors: %d",
		snap.FoldersSynced, snap.FoldersCreated, snap.FoldersDeleted,
		snap.MessagesCopied, snap.MessagesSkipped, snap.MessagesSkippedRegex, snap.MessagesFlagged,
		snap.MessagesUnidentified, snap.MessagesDeleted1, snap.MessagesDeleted2,
		snap.BytesCopied, snap.Errors,
	)
}

// FinalReport формирует итоговый отчёт в стиле imapsync.
func (s *Stats) FinalReport() string {
	snap := s.Get()
	good := snap.Errors == 0
	lines := []string{}
	// Строка «Messages copied: N» — её извлекает парсер отчёта агента
	// (taskexecutor.extractMessagesCount), формат фиксирован.
	lines = append(lines, fmt.Sprintf("Messages copied: %d", snap.MessagesCopied))
	lines = append(lines, fmt.Sprintf(
		"Host1 total size: %d bytes; Host2 total size: %d bytes",
		snap.BytesHost1, snap.BytesHost2))
	lines = append(lines, fmt.Sprintf(
		"Synced %d folders (%d created, %d deleted)",
		snap.FoldersSynced, snap.FoldersCreated, snap.FoldersDeleted))
	lines = append(lines, fmt.Sprintf(
		"Copied %d messages (%d bytes), skipped %d duplicates, skipped %d by regex, resynced flags on %d, %d unidentified",
		snap.MessagesCopied, snap.BytesCopied, snap.MessagesSkipped,
		snap.MessagesSkippedRegex, snap.MessagesFlagged, snap.MessagesUnidentified))
	lines = append(lines, fmt.Sprintf(
		"Deleted %d messages on host1, %d messages on host2",
		snap.MessagesDeleted1, snap.MessagesDeleted2))
	if good {
		lines = append(lines, "The sync looks good, all identified messages in host1 are on host2.")
	} else {
		lines = append(lines, fmt.Sprintf("Detected %d errors", snap.Errors))
	}
	lines = append(lines, fmt.Sprintf("Elapsed time: %s", snap.Elapsed.Round(time.Second)))
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}
