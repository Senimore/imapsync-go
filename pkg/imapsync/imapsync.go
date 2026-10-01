// Package imapsync — публичный фасад библиотеки: точка входа для
// использования imapsync-go как модуля в сторонних проектах.
//
// Пример (полный цикл по аргументам командной строки):
//
//	import "github.com/Senimore/imapsync-go/pkg/imapsync"
//
//	code := imapsync.Run(os.Args[1:], os.Stdout)
//	os.Exit(code)
//
// Пример (программный вызов без разбора аргументов):
//
//	opts, err := options.Parse([]string{"--host1", ..., "--host2", ...})
//	code := imapsync.RunSync(opts, os.Stdout)
package imapsync

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/Senimore/imapsync-go/pkg/imapx"
	"github.com/Senimore/imapsync-go/pkg/logging"
	"github.com/Senimore/imapsync-go/pkg/options"
	"github.com/Senimore/imapsync-go/pkg/report"
	"github.com/Senimore/imapsync-go/pkg/sync"
)

// Коды выхода в стиле imapsync.
const (
	ExOK        = 0   // успешное завершение
	ExSoftware  = 100 // программная ошибка (соединение, логин, ошибки синка)
	ExCantCreat = 73  // нельзя создать файл журнала
	ExUsage     = 64  // ошибка разбора/валидации опций
)

// Run выполняет полный цикл синхронизации по аргументам командной строки
// (разбор, валидация, соединения, синхронизация, отчёт). Возвращает код
// выхода процесса. stdout — куда писать прогресс (обычно os.Stdout).
//
// Функция перехватывает SIGINT/SIGTERM для корректного прерывания
// синхронизации; при программном использовании это безопасно, т.к.
// обработчик снимается через signal.Reset перед возвратом.
func Run(args []string, stdout io.Writer) int {
	opts, err := options.Parse(args)
	if err != nil {
		fmt.Fprintln(stderrOf(stdout), "Ошибка разбора опций:", err)
		fmt.Fprintln(stderrOf(stdout), options.Usage())
		return ExUsage
	}

	if opts.ShowHelp {
		fmt.Fprint(stdout, options.Usage())
		return ExOK
	}
	if opts.ShowVersion {
		fmt.Fprintf(stdout, "imapsync-go %s\n", options.Version)
		return ExOK
	}
	if opts.JustBanner {
		fmt.Fprintf(stdout, "imapsync-go %s\n", options.Version)
		return ExOK
	}

	code := RunSync(opts, stdout)
	if code == ExUsage {
		fmt.Fprintln(stderrOf(stdout), options.Usage())
	}
	return code
}

// RunSync выполняет полный цикл синхронизации с готовыми опциями
// (программный вызов без разбора аргументов). Возвращает код выхода.
func RunSync(opts *options.Options, stdout io.Writer) int {
	if err := opts.Validate(); err != nil {
		fmt.Fprintln(stderrOf(stdout), "Ошибка:", err)
		return ExUsage
	}

	// Порты по умолчанию.
	if opts.Port1 == 0 {
		if opts.SSL1 {
			opts.Port1 = 993
		} else {
			opts.Port1 = 143
		}
	}
	if opts.Port2 == 0 {
		if opts.SSL2 {
			opts.Port2 = 993
		} else {
			opts.Port2 = 143
		}
	}

	// Журнал.
	defLog := logging.DefaultLogPath(opts.User1, opts.User2, opts.Logdir)
	log, err := logging.New(stdout, opts.Logfile, opts.NoLog, opts.Debug, defLog)
	if err != nil {
		fmt.Fprintln(stderrOf(stdout), "Ошибка журнала:", err)
		return ExCantCreat
	}
	defer log.Close()

	log.Printf("imapsync-go %s\n", options.Version)
	log.Printf("host1: %s:%d user1=%s ssl=%v tls=%v\n", opts.Host1, opts.Port1, opts.User1, opts.SSL1, opts.TLS1)
	log.Printf("host2: %s:%d user2=%s ssl=%v tls=%v\n", opts.Host2, opts.Port2, opts.User2, opts.SSL2, opts.TLS2)
	if opts.Dry {
		log.Printf("Режим --dry: изменения не записываются\n")
	}

	timeout := time.Duration(opts.TimeoutSec) * time.Second

	// ВАЖНО: тип должен быть io.Writer, иначе nil *os.File, упакованный в
	// интерфейс, пройдёт проверку != nil и go-imap будет писать отладку в
	// nil-файл, что даёт ошибку "invalid argument".
	var debugW1, debugW2 io.Writer
	if opts.DebugImap {
		debugW1 = stdout
		debugW2 = stdout
	}

	src, err := imapx.Connect(opts.Host1, opts.Port1, opts.SSL1, opts.TLS1, opts.Insecure, timeout, debugW1)
	if err != nil {
		log.Printf("Не удалось подключиться к host1: %v\n", err)
		return ExSoftware
	}
	defer src.Logout()

	dst, err := imapx.Connect(opts.Host2, opts.Port2, opts.SSL2, opts.TLS2, opts.Insecure, timeout, debugW2)
	if err != nil {
		log.Printf("Не удалось подключиться к host2: %v\n", err)
		return ExSoftware
	}
	defer dst.Logout()

	log.Printf("Capabilities host1: %s\n", CapsString(src.Caps))
	log.Printf("Capabilities host2: %s\n", CapsString(dst.Caps))

	if opts.JustConnect {
		log.Printf("--justconnect: соединение установлено, выход\n")
		return ExOK
	}

	// Логин.
	if err := src.Login(opts.User1, opts.Password1, opts.AuthMech1, opts.AuthUser1, opts.ProxyAuth1); err != nil {
		log.Printf("%v\n", err)
		return ExSoftware
	}
	log.Printf("Логин host1 успешен: %s\n", opts.User1)

	if err := dst.Login(opts.User2, opts.Password2, opts.AuthMech2, opts.AuthUser2, opts.ProxyAuth2); err != nil {
		log.Printf("%v\n", err)
		return ExSoftware
	}
	log.Printf("Логин host2 успешен: %s\n", opts.User2)

	if opts.JustLogin {
		log.Printf("--justlogin: вход выполнен, выход\n")
		return ExOK
	}

	// Спец-режимы по папкам.
	if opts.JustFolders || opts.JustFoldersizes {
		return JustFolders(src, log, opts.JustFoldersizes)
	}

	// Полная синхронизация.
	stats := report.New()
	s := sync.New(src, dst, opts, log, stats)

	// Для параллельного режима (--threads>1) каждой горутине нужна своя пара
	// соединений, т.к. у одного IMAP-соединения открыта одна папка (SELECT).
	if opts.Threads > 1 {
		s.NewPair = func() (*imapx.Conn, *imapx.Conn, error) {
			s1, err := imapx.Connect(opts.Host1, opts.Port1, opts.SSL1, opts.TLS1, opts.Insecure, timeout, nil)
			if err != nil {
				return nil, nil, fmt.Errorf("host1 pair: %w", err)
			}
			if err := s1.Login(opts.User1, opts.Password1, opts.AuthMech1, opts.AuthUser1, opts.ProxyAuth1); err != nil {
				s1.Logout()
				return nil, nil, fmt.Errorf("host1 pair login: %w", err)
			}
			s2, err := imapx.Connect(opts.Host2, opts.Port2, opts.SSL2, opts.TLS2, opts.Insecure, timeout, nil)
			if err != nil {
				s1.Logout()
				return nil, nil, fmt.Errorf("host2 pair: %w", err)
			}
			if err := s2.Login(opts.User2, opts.Password2, opts.AuthMech2, opts.AuthUser2, opts.ProxyAuth2); err != nil {
				s1.Logout()
				s2.Logout()
				return nil, nil, fmt.Errorf("host2 pair login: %w", err)
			}
			return s1, s2, nil
		}
	}

	// Обработка сигналов: Ctrl-C — прервать синхронизацию.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sigCh {
			log.Printf("Получен сигнал — прерываю синхронизацию...\n")
			s.Abort()
		}
	}()

	runErr := s.Run()
	// Снимаем подписку и закрываем канал, чтобы горутина завершилась
	// (signal.Stop гарантирует, что по каналу больше не будет отправок).
	signal.Stop(sigCh)
	close(sigCh)

	// Итоговый отчёт.
	log.Printf("\n%s\n", stats.FinalReport())
	if runErr != nil {
		log.Printf("Завершено с ошибкой: %v\n", runErr)
	}

	snap := stats.Get()
	log.Printf("Exiting with return value %d (EX_OK: successful termination) %d/%d nb_errors/max_errors PID %d\n",
		ExitCode(snap.Errors), snap.Errors, opts.ErrorsMax, os.Getpid())
	if p := log.LogPath(); p != "" {
		log.Printf("Log file is %s ( to change it, use --logfile filepath ; or use --nolog to turn off logging )\n", p)
	}

	if runErr != nil {
		return ExSoftware
	}
	if snap.Errors > 0 {
		return ExSoftware
	}
	return ExOK
}

// JustFolders печатает список (и размеры) папок источника.
func JustFolders(src *imapx.Conn, log *logging.Logger, withSizes bool) int {
	folders, err := src.ListFolders()
	if err != nil {
		log.Printf("LIST host1: %v\n", err)
		return ExSoftware
	}
	sort.Slice(folders, func(i, j int) bool { return folders[i].Name < folders[j].Name })
	total := 0
	for _, f := range folders {
		if withSizes {
			st, err := src.Status(f.Name)
			if err != nil {
				log.Printf("%-40s (status: %v)\n", f.Name, err)
				continue
			}
			log.Printf("%-40s messages=%d unseen=%d uidvalidity=%d\n",
				f.Name, st.Messages, st.Unseen, st.UidValidity)
			total += int(st.Messages)
		} else {
			log.Printf("%s\n", f.Name)
		}
	}
	if withSizes {
		log.Printf("Всего сообщений на host1: %d\n", total)
	}
	return ExOK
}

// CapsString форматирует карту возможностей IMAP в строку.
func CapsString(caps map[string]bool) string {
	if len(caps) == 0 {
		return "(нет данных)"
	}
	keys := make([]string, 0, len(caps))
	for k := range caps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += " "
		}
		out += k
	}
	return out
}

// ExitCode возвращает код выхода по числу ошибок.
func ExitCode(errors int64) int {
	if errors == 0 {
		return ExOK
	}
	return ExSoftware
}

// stderrOf возвращает os.Stderr (stdout используется только для прогресса).
func stderrOf(_ io.Writer) io.Writer {
	return os.Stderr
}
