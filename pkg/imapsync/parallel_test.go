package imapsync_test

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/server"

	"github.com/Senimore/imapsync-go/pkg/imapsync"
	"github.com/Senimore/imapsync-go/pkg/imapx"
)

// TestRunSyncInProcessParallel проверяет, что движок можно запускать
// параллельно из нескольких горутин (распараллеливание миграции по
// аккаунтам): каждый запуск — своя пара in-memory серверов, свои
// соединения, свой вывод.
//
// Запускается только без -race: backend/memory из go-imap не является
// потокобезопасным (его мапы не защищены мьютексами), поэтому гонки
// возникают в тестовом бэкенде, а не в коде синхронизации.
func TestRunSyncInProcessParallel(t *testing.T) {
	if raceEnabled() {
		t.Skip("backend/memory не потокобезопасен; пропуск при -race")
	}

	const n = 4 // параллельных синхронизаций (аккаунтов)

	type run struct {
		code int
		out  *bytes.Buffer
	}
	runs := make([]run, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			src := startTestServer(t)
			dst := startTestServer(t)

			// Источник: INBOX + папка Work, по 3 письма в каждую.
			if err := seedServer(src, "INBOX", idx, 3); err != nil {
				t.Errorf("seed INBOX: %v", err)
				return
			}
			if err := seedServerFolder(src, "Work", idx, 3); err != nil {
				t.Errorf("seed Work: %v", err)
				return
			}

			out := &bytes.Buffer{}
			runs[idx].out = out

			args := []string{
				"--host1", src.host, "--port1", fmt.Sprint(src.port),
				"--user1", "username", "--password1", "password",
				"--host2", dst.host, "--port2", fmt.Sprint(dst.port),
				"--user2", "username", "--password2", "password",
				"--nolog", "--useheader", "Message-Id", "--threads", "1",
			}
			runs[idx].code = imapsync.RunSyncInProcess(args, out, out)
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if runs[i].out == nil {
			t.Fatalf("запуск %d: вывод не записан", i)
		}
		if runs[i].code != imapsync.ExOK {
			t.Errorf("запуск %d: код выхода %d, want %d\nвывод:\n%s",
				i, runs[i].code, imapsync.ExOK, runs[i].out.String())
			continue
		}
		// Каждый запуск копирует свои письма: 3 в INBOX + 3 в Work.
		want := fmt.Sprintf("Messages copied: %d", 6)
		if !strings.Contains(runs[i].out.String(), want) {
			t.Errorf("запуск %d: в отчёте нет %q\nвывод:\n%s",
				i, want, runs[i].out.String())
		}
	}
}

// raceEnabled — включён ли флаг -race.
func raceEnabled() bool {
	f := flag.Lookup("race")
	return f != nil && f.Value.String() == "true"
}

type testServer struct {
	srv  *server.Server
	host string
	port int
}

func startTestServer(t *testing.T) *testServer {
	t.Helper()

	be := memory.New()
	srv := server.New(be)
	srv.AllowInsecureAuth = true

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })

	addr := l.Addr().(*net.TCPAddr)
	return &testServer{srv: srv, host: addr.IP.String(), port: addr.Port}
}

func (ts *testServer) connect() (*imapx.Conn, error) {
	c, err := imapx.Connect(ts.host, ts.port, false, false, false, 10*time.Second, io.Discard)
	if err != nil {
		return nil, err
	}
	if err := c.Login("username", "password", "LOGIN", "", false); err != nil {
		c.Logout()
		return nil, err
	}
	return c, nil
}

// seedServer добавляет count писем в существующую папку (INBOX).
func seedServer(ts *testServer, folder string, run, count int) error {
	c, err := ts.connect()
	if err != nil {
		return err
	}
	defer c.Logout()
	return appendMsgs(c, folder, run, count)
}

// seedServerFolder создаёт папку и добавляет в неё count писем.
func seedServerFolder(ts *testServer, folder string, run, count int) error {
	c, err := ts.connect()
	if err != nil {
		return err
	}
	defer c.Logout()
	if err := c.Create(folder); err != nil {
		return err
	}
	return appendMsgs(c, folder, run, count)
}

func appendMsgs(c *imapx.Conn, folder string, run, count int) error {
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("p%d-%s-%d", run, folder, i)
		body := []byte("From: src@example.org\r\n" +
			"To: dst@example.org\r\n" +
			"Subject: " + id + "\r\n" +
			"Message-Id: <" + id + "@localhost>\r\n" +
			"Date: Wed, 11 May 2016 14:31:59 +0000\r\n" +
			"Content-Type: text/plain\r\n" +
			"\r\n" +
			"Body " + id + "\r\n")
		if err := c.AppendMessage(folder, nil, time.Now(), body); err != nil {
			return fmt.Errorf("append %s: %w", id, err)
		}
	}
	return nil
}
