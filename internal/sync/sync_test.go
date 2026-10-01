package sync

import (
	"testing"
	"time"

	"github.com/example/imapsync-go/internal/imapx"
	"github.com/example/imapsync-go/internal/options"
)

func newTestSync(t *testing.T, headers []string) *Sync {
	t.Helper()
	o := options.New()
	o.UseHeader = headers
	return New(nil, nil, o, nil, nil)
}

func TestCanonicalHeader(t *testing.T) {
	cases := map[string]string{
		"message-id": "Message-Id",
		"Message-Id": "Message-Id",
		"MESSAGE-ID": "Message-Id",
		"received":   "Received",
		"":           "",
	}
	for in, want := range cases {
		if got := canonicalHeader(in); got != want {
			t.Fatalf("canonicalHeader(%q)=%q want %q", in, got, want)
		}
	}
}

func TestIdentityKey(t *testing.T) {
	s := newTestSync(t, []string{"Message-Id"})
	info := &imapx.HeaderInfo{
		Values: map[string][]string{
			"Message-Id": {"<abc@x>"},
		},
	}
	key := s.identityKey(info)
	if key == "" {
		t.Fatalf("expected non-empty identity key")
	}
	// Пустой заголовок -> пустой ключ (неидентифицировано).
	empty := &imapx.HeaderInfo{Values: map[string][]string{}}
	if s.identityKey(empty) != "" {
		t.Fatalf("expected empty key for no headers")
	}
}

func TestIdentityKeyMultipleHeaders(t *testing.T) {
	s := newTestSync(t, []string{"Message-Id", "Received"})
	info := &imapx.HeaderInfo{
		Values: map[string][]string{
			"Message-Id": {"<a>"},
			"Received":   {"from x", "from y"},
		},
	}
	key := s.identityKey(info)
	if key == "" {
		t.Fatalf("expected non-empty key")
	}
	// Ключ должен содержать оба заголовка.
	if !contains(key, "Message-Id:<a>") || !contains(key, "Received:from x,from y") {
		t.Fatalf("key missing parts: %q", key)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestMapFolderDefault(t *testing.T) {
	s := newTestSync(t, []string{"Message-Id"})
	got := s.mapFolder("INBOX", "/", "/", map[string]string{})
	if got != "INBOX" {
		t.Fatalf("mapFolder INBOX=%q", got)
	}
	got = s.mapFolder("Archive/2020", "/", "/", map[string]string{})
	if got != "Archive/2020" {
		t.Fatalf("mapFolder nested=%q", got)
	}
}

func TestMapFolderDelim(t *testing.T) {
	s := newTestSync(t, []string{"Message-Id"})
	got := s.mapFolder("Archive/2020", "/", ".", map[string]string{})
	if got != "Archive.2020" {
		t.Fatalf("mapFolder delim=%q", got)
	}
}

func TestMapFolderF1F2(t *testing.T) {
	s := newTestSync(t, []string{"Message-Id"})
	m := map[string]string{"INBOX": "INBOX2"}
	got := s.mapFolder("INBOX", "/", "/", m)
	if got != "INBOX2" {
		t.Fatalf("mapFolder f1f2=%q", got)
	}
}

func TestMapFolderSubfolder(t *testing.T) {
	s := newTestSync(t, []string{"Message-Id"})
	s.Opts.Subfolder1 = "Archive"
	s.Opts.Subfolder2 = "Backup"
	got := s.mapFolder("Archive/2020", "/", "/", map[string]string{})
	if got != "Backup/2020" {
		t.Fatalf("mapFolder subfolder=%q", got)
	}
}

func TestNormalizeAndSameFlags(t *testing.T) {
	a := normalizeFlags([]string{"\\Seen", "\\Seen", "\\Flagged"})
	if len(a) != 2 {
		t.Fatalf("normalize dedup: %v", a)
	}
	if !sameFlags(a, []string{"\\Flagged", "\\Seen"}) {
		t.Fatalf("sameFlags should ignore order")
	}
	if sameFlags(a, []string{"\\Seen"}) {
		t.Fatalf("sameFlags should detect difference")
	}
}

func TestAddHeader(t *testing.T) {
	out := addHeader([]byte("Subject: hi\r\n\r\nbody"), "X-IMAPSYNC", "v1")
	if len(out) < len("X-IMAPSYNC: v1\r\n") {
		t.Fatalf("addHeader too short")
	}
	if string(out[:len("X-IMAPSYNC: v1\r\n")]) != "X-IMAPSYNC: v1\r\n" {
		t.Fatalf("addHeader prefix: %q", string(out[:20]))
	}
}

func TestPassFilters(t *testing.T) {
	s := newTestSync(t, []string{"Message-Id"})
	s.Opts.MinSize = 100
	s.Opts.MaxSize = 1000
	info := &imapx.HeaderInfo{Size: 500, Date: time.Now()}
	if !s.passFilters(info) {
		t.Fatalf("500 should pass [100,1000]")
	}
	info.Size = 50
	if s.passFilters(info) {
		t.Fatalf("50 should fail minsize")
	}
	info.Size = 2000
	if s.passFilters(info) {
		t.Fatalf("2000 should fail maxsize")
	}
}

func TestPassFiltersAge(t *testing.T) {
	s := newTestSync(t, []string{"Message-Id"})
	s.Opts.MaxAge = 10 // дней
	old := &imapx.HeaderInfo{Date: time.Now().Add(-30 * 24 * time.Hour)}
	if s.passFilters(old) {
		t.Fatalf("30d message should fail maxage=10")
	}
	fresh := &imapx.HeaderInfo{Date: time.Now().Add(-2 * 24 * time.Hour)}
	if !s.passFilters(fresh) {
		t.Fatalf("2d message should pass maxage=10")
	}
}
