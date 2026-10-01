package sync_test

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/server"

	"github.com/example/imapsync-go/internal/imapx"
	"github.com/example/imapsync-go/internal/logging"
	"github.com/example/imapsync-go/internal/options"
	"github.com/example/imapsync-go/internal/report"
	"github.com/example/imapsync-go/internal/sync"
)

// testServer — in-memory IMAP-сервер на эфемерном порту.
type testServer struct {
	srv  *server.Server
	host string
	port int
}

func startServer(t *testing.T) *testServer {
	t.Helper()

	be := memory.New()
	srv := server.New(be)
	srv.AllowInsecureAuth = true

	if os.Getenv("IMAPSYNC_TEST_DEBUG") != "" {
		testDebug = os.Stderr
		srv.Debug = os.Stderr
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })

	addr := l.Addr().(*net.TCPAddr)
	return &testServer{srv: srv, host: addr.IP.String(), port: addr.Port}
}

// testDebug — куда писать протокольный дамп (устанавливается из
// IMAPSYNC_TEST_DEBUG для отладки интеграционных тестов).
var testDebug io.Writer = io.Discard

func (ts *testServer) connect(t *testing.T) *imapx.Conn {
	t.Helper()
	c, err := imapx.Connect(ts.host, ts.port, false, false, false, 10*time.Second, testDebug)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Logout() })
	if err := c.Login("username", "password", "LOGIN", "", false); err != nil {
		t.Fatalf("login: %v", err)
	}
	return c
}

func msgBody(msgID, subject string) []byte {
	return []byte("From: src@example.org\r\n" +
		"To: dst@example.org\r\n" +
		"Subject: " + subject + "\r\n" +
		"Message-Id: <" + msgID + "@localhost>\r\n" +
		"Date: Wed, 11 May 2016 14:31:59 +0000\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Body of " + subject + "\r\n")
}

func seed(t *testing.T, c *imapx.Conn, folder string, flags []string, msgIDs ...string) {
	t.Helper()
	for _, id := range msgIDs {
		if err := c.AppendMessage(folder, flags, time.Now(), msgBody(id, "S-"+id)); err != nil {
			t.Fatalf("seed append %s: %v", id, err)
		}
	}
}

func newTestOpts() *options.Options {
	o := options.New()
	o.Host1 = "127.0.0.1"
	o.Host2 = "127.0.0.1"
	o.User1 = "username"
	o.User2 = "username"
	o.Password1 = "password"
	o.Password2 = "password"
	o.NoLog = true
	o.Threads = 1
	return o
}

func newTestSync(t *testing.T, src, dst *imapx.Conn, opts *options.Options) *sync.Sync {
	t.Helper()
	log, err := logging.New(io.Discard, "", true, false, "")
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	t.Cleanup(log.Close)
	return sync.New(src, dst, opts, log, report.New())
}

func folderMessages(t *testing.T, c *imapx.Conn, folder string) (int, []string) {
	t.Helper()
	if _, err := c.Select(folder, false); err != nil {
		t.Fatalf("select %q: %v", folder, err)
	}
	uids, err := c.UidSearchAll("")
	if err != nil {
		t.Fatalf("search %q: %v", folder, err)
	}
	return len(uids), uidsToStrings(uids)
}

func uidsToStrings(uids []uint32) []string {
	out := make([]string, 0, len(uids))
	for _, u := range uids {
		out = append(out, fmt.Sprint(u))
	}
	return out
}

func TestIntegrationSyncInbox(t *testing.T) {
	srcSrv := startServer(t)
	dstSrv := startServer(t)

	src := srcSrv.connect(t)
	dst := dstSrv.connect(t)

	// Источник: 3 сообщения (одно уже есть на назначении).
	seed(t, src, "INBOX", []string{imap.SeenFlag, imap.FlaggedFlag}, "m1", "m2", "m3")
	seed(t, dst, "INBOX", nil, "m2")

	opts := newTestOpts()
	s := newTestSync(t, src, dst, opts)

	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	snap := s.Stats.Get()
	// memory.New() сеет одинаковое дефолтное сообщение на обоих серверах,
	// поэтому: src = дефолт+m1+m2+m3, dst = дефолт+m2.
	// Копируются m1,m3 (2); пропускаются дефолт,m2 (2).
	if snap.MessagesCopied != 2 {
		t.Errorf("MessagesCopied = %d, want 2", snap.MessagesCopied)
	}
	if snap.MessagesSkipped != 2 {
		t.Errorf("MessagesSkipped = %d, want 2", snap.MessagesSkipped)
	}

	n, _ := folderMessages(t, dst, "INBOX")
	if n != 4 {
		t.Errorf("INBOX host2: %d сообщений, want 4", n)
	}

	// Повторный запуск идемпотентен: ничего не копируем.
	src2 := srcSrv.connect(t)
	dst2 := dstSrv.connect(t)
	s2 := newTestSync(t, src2, dst2, newTestOpts())
	if err := s2.Run(); err != nil {
		t.Fatalf("Run2: %v", err)
	}
	snap2 := s2.Stats.Get()
	if snap2.MessagesCopied != 0 {
		t.Errorf("повтор: MessagesCopied = %d, want 0", snap2.MessagesCopied)
	}
	if snap2.MessagesSkipped != 4 {
		t.Errorf("повтор: MessagesSkipped = %d, want 4", snap2.MessagesSkipped)
	}
}

func TestIntegrationFlagsPreserved(t *testing.T) {
	srcSrv := startServer(t)
	dstSrv := startServer(t)

	src := srcSrv.connect(t)
	dst := dstSrv.connect(t)

	seed(t, src, "INBOX", []string{imap.SeenFlag, imap.FlaggedFlag, imap.DraftFlag}, "f1")

	s := newTestSync(t, src, dst, newTestOpts())
	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Проверяем флаги скопированного сообщения на host2.
	if _, err := dst.Select("INBOX", false); err != nil {
		t.Fatalf("select: %v", err)
	}
	uids, err := dst.UidSearchAll("")
	if err != nil || len(uids) != 2 {
		t.Fatalf("ожидалось 2 сообщения на host2 (дефолт + f1), получено %d (err=%v)", len(uids), err)
	}
	m, err := dst.FetchHeaderMap(uids, []string{"Message-Id"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	var info *imapx.HeaderInfo
	for _, u := range uids {
		cand := m[u]
		if cand != nil && strings.Contains(strings.Join(cand.Values["Message-Id"], ","), "f1") {
			info = cand
			break
		}
	}
	if info == nil {
		t.Fatalf("сообщение f1 не найдено на host2")
	}
	got := map[string]bool{}
	for _, f := range info.Flags {
		got[imap.CanonicalFlag(f)] = true
	}
	for _, want := range []string{imap.SeenFlag, imap.FlaggedFlag, imap.DraftFlag} {
		if !got[want] {
			t.Errorf("флаг %s отсутствует на host2: %v", want, info.Flags)
		}
	}
}

func TestIntegrationFolderMapping(t *testing.T) {
	srcSrv := startServer(t)
	dstSrv := startServer(t)

	src := srcSrv.connect(t)
	dst := dstSrv.connect(t)

	// Создаём папку на источнике и кладём сообщение.
	if err := src.Create("Archive"); err != nil {
		t.Fatalf("create: %v", err)
	}
	seed(t, src, "Archive", nil, "a1", "a2")

	opts := newTestOpts()
	opts.F1F2 = [][2]string{{"Archive", "Archived"}}

	s := newTestSync(t, src, dst, opts)
	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	n, _ := folderMessages(t, dst, "Archived")
	if n != 2 {
		t.Errorf("Archived host2: %d сообщений, want 2", n)
	}
	snap := s.Stats.Get()
	if snap.FoldersCreated < 1 {
		t.Errorf("FoldersCreated = %d, want >=1", snap.FoldersCreated)
	}
}

func TestIntegrationDelete2(t *testing.T) {
	srcSrv := startServer(t)
	dstSrv := startServer(t)

	src := srcSrv.connect(t)
	dst := dstSrv.connect(t)

	seed(t, src, "INBOX", nil, "d1")
	seed(t, dst, "INBOX", nil, "d1", "extra")

	opts := newTestOpts()
	opts.Delete2 = true
	opts.Expunge2 = true

	s := newTestSync(t, src, dst, opts)
	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	snap := s.Stats.Get()
	// src = дефолт+d1; dst = дефолт+d1+extra. В источнике нет только extra.
	if snap.MessagesDeleted2 != 1 {
		t.Errorf("MessagesDeleted2 = %d, want 1", snap.MessagesDeleted2)
	}

	n, _ := folderMessages(t, dst, "INBOX")
	if n != 2 {
		t.Errorf("INBOX host2 после delete2: %d сообщений, want 2", n)
	}
}

func TestIntegrationDry(t *testing.T) {
	srcSrv := startServer(t)
	dstSrv := startServer(t)

	src := srcSrv.connect(t)
	dst := dstSrv.connect(t)

	seed(t, src, "INBOX", nil, "dry1", "dry2")

	opts := newTestOpts()
	opts.Dry = true

	s := newTestSync(t, src, dst, opts)
	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	snap := s.Stats.Get()
	// src = дефолт+dry1+dry2; дефолт уже есть на dst -> копируются 2.
	if snap.MessagesCopied != 2 {
		t.Errorf("MessagesCopied = %d, want 2", snap.MessagesCopied)
	}
	n, _ := folderMessages(t, dst, "INBOX")
	if n != 1 {
		// memory.New() сеет 1 сообщение по умолчанию; dry ничего не добавляет.
		t.Errorf("INBOX host2 после dry: %d сообщений, want 1 (без изменений)", n)
	}
}

// TestIntegrationThreads проверяет параллельный режим (--threads>1).
// Запускается только без -race: backend/memory из go-imap не является
// потокобезопасным (его мапы не защищены мьютексами), поэтому гонки
// возникают в тестовом бэкенде, а не в коде синхронизации.
func TestIntegrationThreads(t *testing.T) {
	if raceEnabled {
		t.Skip("backend/memory не потокобезопасен; пропуск при -race")
	}
	srcSrv := startServer(t)
	dstSrv := startServer(t)

	src := srcSrv.connect(t)
	dst := dstSrv.connect(t)

	for _, f := range []string{"A", "B", "C"} {
		if err := src.Create(f); err != nil {
			t.Fatalf("create %s: %v", f, err)
		}
		seed(t, src, f, nil, "t-"+f+"-1", "t-"+f+"-2")
	}

	opts := newTestOpts()
	opts.Threads = 3

	s := newTestSync(t, src, dst, opts)
	s.NewPair = func() (*imapx.Conn, *imapx.Conn, error) {
		a, err := srcSrv.connect2()
		if err != nil {
			return nil, nil, err
		}
		b, err := dstSrv.connect2()
		if err != nil {
			a.Logout()
			return nil, nil, err
		}
		return a, b, nil
	}

	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	snap := s.Stats.Get()
	// 3 папки по 2 сообщения + INBOX (дефолт источника уже есть на назначении).
	if snap.MessagesCopied != 6 {
		t.Errorf("MessagesCopied = %d, want 6", snap.MessagesCopied)
	}

	for _, f := range []string{"A", "B", "C"} {
		n, _ := folderMessages(t, dst, f)
		if n != 2 {
			t.Errorf("папка %s host2: %d сообщений, want 2", f, n)
		}
	}
}

// connect2 — подключение без t.Cleanup (для пула соединений в параллельном
// режиме; соединения закрываются самим Sync через Logout).
func (ts *testServer) connect2() (*imapx.Conn, error) {
	c, err := imapx.Connect(ts.host, ts.port, false, false, false, 10*time.Second, testDebug)
	if err != nil {
		return nil, err
	}
	if err := c.Login("username", "password", "LOGIN", "", false); err != nil {
		c.Logout()
		return nil, err
	}
	return c, nil
}

func TestIntegrationDelete2Folders(t *testing.T) {
	srcSrv := startServer(t)
	dstSrv := startServer(t)

	src := srcSrv.connect(t)
	dst := dstSrv.connect(t)

	if err := dst.Create("Old"); err != nil {
		t.Fatalf("create: %v", err)
	}

	opts := newTestOpts()
	opts.Delete2Folders = true

	s := newTestSync(t, src, dst, opts)
	if err := s.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	folders, err := dst.ListFolders()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, f := range folders {
		if strings.EqualFold(f.Name, "Old") {
			t.Errorf("папка Old не удалена с host2")
		}
	}
	snap := s.Stats.Get()
	if snap.FoldersDeleted != 1 {
		t.Errorf("FoldersDeleted = %d, want 1", snap.FoldersDeleted)
	}
}
