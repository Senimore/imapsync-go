// Package logging реализует двойной вывод: в stdout и в файл журнала,
// как это делает imapsync (опции --logfile/--nolog/--debug).
package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Logger пишет сообщения одновременно в консоль и (опционально) в файл.
type Logger struct {
	mu      sync.Mutex
	w       io.Writer // stdout
	file    *os.File
	fileW   io.Writer
	noFile  bool
	debugOn bool
}

// DefaultLogPath формирует путь журнала в стиле imapsync:
// LOG_imapsync/<stamp>_<user1>_<user2>.txt
func DefaultLogPath(user1, user2, logdir string) string {
	if logdir == "" {
		logdir = "LOG_imapsync"
	}
	stamp := time.Now().Format("2006_01_02_15_04_05_000")
	name := fmt.Sprintf("%s_%s_%s.txt", stamp, sanitize(user1), sanitize(user2))
	return filepath.Join(logdir, name)
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, " ", "_")
	if s == "" {
		s = "user"
	}
	return s
}

// New создаёт Logger. Если logfile пустой и noLog=false, используется
// defaultPath. Вызовы безопасны для конкурентного использования.
func New(stdout io.Writer, logfile string, noLog, debug bool, defaultPath string) (*Logger, error) {
	l := &Logger{w: stdout, debugOn: debug}
	if noLog {
		l.noFile = true
		return l, nil
	}
	path := logfile
	if path == "" {
		path = defaultPath
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("не удалось создать каталог журнала %q: %w", dir, err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть файл журнала %q: %w", path, err)
	}
	l.file = f
	l.fileW = f
	return l, nil
}

// LogPath возвращает путь к файлу журнала (пусто, если журнал отключён).
func (l *Logger) LogPath() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return ""
	}
	return l.file.Name()
}

// Printf пишет форматированное сообщение в stdout и журнал.
func (l *Logger) Printf(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprint(l.w, msg)
	if l.fileW != nil {
		fmt.Fprint(l.fileW, msg)
	}
}

// Debugf пишет сообщение только если включён --debug.
func (l *Logger) Debugf(format string, args ...interface{}) {
	if !l.debugOn {
		return
	}
	l.Printf(format, args...)
}

// Close закрывает файл журнала.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
		l.fileW = nil
	}
}
