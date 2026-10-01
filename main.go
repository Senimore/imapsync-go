// Command imapsync-go — синхронизация двух IMAP-аккаунтов (аналог imapsync).
//
// Пример:
//
//	imapsync-go --host1 imap.src.ru --user1 a --password1 p1 \
//	            --host2 imap.dst.ru --user2 b --password2 p2 \
//	            --ssl1 --ssl2 --useheader Message-Id
package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/example/imapsync-go/internal/imapx"
	"github.com/example/imapsync-go/internal/logging"
	"github.com/example/imapsync-go/internal/options"
	"github.com/example/imapsync-go/internal/report"
	"github.com/example/imapsync-go/internal/sync"
)

// exit codes в стиле imapsync.
const (
	EX_OK        = 0
	EX_SOFTWARE  = 100
	EX_CANTCREAT = 73
	EX_USAGE     = 64
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	opts, err := options.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка разбора опций:", err)
		fmt.Fprintln(os.Stderr, options.Usage())
		return EX_USAGE
	}

	if opts.ShowHelp {
		fmt.Print(options.Usage())
		return EX_OK
	}
	if opts.ShowVersion {
		fmt.Printf("imapsync-go %s\n", options.Version)
		return EX_OK
	}
	if opts.JustBanner {
		fmt.Printf("imapsync-go %s\n", options.Version)
		return EX_OK
	}

	if err := opts.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		fmt.Fprintln(os.Stderr, options.Usage())
		return EX_USAGE
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
	log, err := logging.New(os.Stdout, opts.Logfile, opts.NoLog, opts.Debug, defLog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка журнала:", err)
		return EX_CANTCREAT
	}
	defer log.Close()

	log.Printf("imapsync-go %s\n", options.Version)
	log.Printf("host1: %s:%d user1=%s ssl=%v tls=%v\n", opts.Host1, opts.Port1, opts.User1, opts.SSL1, opts.TLS1)
	log.Printf("host2: %s:%d user2=%s ssl=%v tls=%v\n", opts.Host2, opts.Port2, opts.User2, opts.SSL2, opts.TLS2)
	if opts.Dry {
		log.Printf("Режим --dry: изменения не записываются\n")
	}

	timeout := time.Duration(opts.TimeoutSec) * time.Second

	// Подключение host1.
	// ВАЖНО: тип должен быть io.Writer, иначе nil *os.File, упакованный в
	// интерфейс, пройдёт проверку != nil и go-imap будет писать отладку в
	// nil-файл, что даёт ошибку "invalid argument".
	var debugW1, debugW2 io.Writer
	if opts.DebugImap {
		debugW1 = os.Stdout
		debugW2 = os.Stdout
	}

	src, err := imapx.Connect(opts.Host1, opts.Port1, opts.SSL1, opts.TLS1, opts.Insecure, timeout, debugW1)
	if err != nil {
		log.Printf("Не удалось подключиться к host1: %v\n", err)
		return EX_SOFTWARE
	}
	defer src.Logout()

	dst, err := imapx.Connect(opts.Host2, opts.Port2, opts.SSL2, opts.TLS2, opts.Insecure, timeout, debugW2)
	if err != nil {
		log.Printf("Не удалось подключиться к host2: %v\n", err)
		return EX_SOFTWARE
	}
	defer dst.Logout()

	log.Printf("Capabilities host1: %s\n", capsString(src.Caps))
	log.Printf("Capabilities host2: %s\n", capsString(dst.Caps))

	if opts.JustConnect {
		log.Printf("--justconnect: соединение установлено, выход\n")
		return EX_OK
	}

	// Логин.
	if err := src.Login(opts.User1, opts.Password1, opts.AuthMech1, opts.AuthUser1, opts.ProxyAuth1); err != nil {
		log.Printf("%v\n", err)
		return EX_SOFTWARE
	}
	log.Printf("Логин host1 успешен: %s\n", opts.User1)

	if err := dst.Login(opts.User2, opts.Password2, opts.AuthMech2, opts.AuthUser2, opts.ProxyAuth2); err != nil {
		log.Printf("%v\n", err)
		return EX_SOFTWARE
	}
	log.Printf("Логин host2 успешен: %s\n", opts.User2)

	if opts.JustLogin {
		log.Printf("--justlogin: вход выполнен, выход\n")
		return EX_OK
	}

	// Спец-режимы по папкам.
	if opts.JustFolders || opts.JustFoldersizes {
		return justFolders(src, log, opts.JustFoldersizes)
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

	// Итоговый отчёт.
	log.Printf("\n%s\n", stats.FinalReport())
	if runErr != nil {
		log.Printf("Завершено с ошибкой: %v\n", runErr)
	}

	snap := stats.Get()
	log.Printf("Exiting with return value %d (EX_OK: successful termination) %d/%d nb_errors/max_errors PID %d\n",
		exitCode(snap.Errors), snap.Errors, opts.ErrorsMax, os.Getpid())
	if p := log.LogPath(); p != "" {
		log.Printf("Log file is %s ( to change it, use --logfile filepath ; or use --nolog to turn off logging )\n", p)
	}

	if runErr != nil {
		return EX_SOFTWARE
	}
	if snap.Errors > 0 {
		return EX_SOFTWARE
	}
	return EX_OK
}

// justFolders печатает список (и размеры) папок источника.
func justFolders(src *imapx.Conn, log *logging.Logger, withSizes bool) int {
	folders, err := src.ListFolders()
	if err != nil {
		log.Printf("LIST host1: %v\n", err)
		return EX_SOFTWARE
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
	return EX_OK
}

func capsString(caps map[string]bool) string {
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

func exitCode(errors int64) int {
	if errors == 0 {
		return EX_OK
	}
	return EX_SOFTWARE
}
