package cache

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCacheSqlite(t *testing.T) {
	dir, err := os.MkdirTemp("", "imapsync_cache_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	c, err := Open(dir, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	if _, _, ok := c.GetMapping("INBOX", 100); ok {
		t.Fatalf("expected not found")
	}

	if err := c.PutMapping("INBOX", 100, 200, "key-msg-1"); err != nil {
		t.Fatalf("PutMapping: %v", err)
	}

	uid2, key, ok := c.GetMapping("INBOX", 100)
	if !ok || uid2 != 200 || key != "key-msg-1" {
		t.Fatalf("got uid2=%d, key=%s, ok=%v", uid2, key, ok)
	}

	// Update mapping
	if err := c.PutMapping("INBOX", 100, 201, "key-msg-2"); err != nil {
		t.Fatalf("PutMapping update: %v", err)
	}
	uid2, key, ok = c.GetMapping("INBOX", 100)
	if !ok || uid2 != 201 || key != "key-msg-2" {
		t.Fatalf("got uid2=%d, key=%s, ok=%v", uid2, key, ok)
	}

	if err := c.DeleteFolder("INBOX"); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if _, _, ok := c.GetMapping("INBOX", 100); ok {
		t.Fatalf("expected not found after DeleteFolder")
	}
}

// TestWALMode проверяет, что база открыта в journal_mode=WAL — режиме,
// в котором читатели не блокируют писателя (нужно для параллельных сессий).
func TestWALMode(t *testing.T) {
	dir, err := os.MkdirTemp("", "imapsync_cache_wal_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	c, err := Open(dir, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	mode, err := c.JournalMode()
	if err != nil {
		t.Fatalf("JournalMode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

// TestNamespaceIsolation проверяет, что разные namespace дают разные файлы:
// параллельные миграции разных пар аккаунтов не пишут в одну базу.
func TestNamespaceIsolation(t *testing.T) {
	dir, err := os.MkdirTemp("", "imapsync_cache_ns_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	a, err := Open(dir, "alice@mail.src.ru__bob@mail.dst.ru")
	if err != nil {
		t.Fatalf("Open(a): %v", err)
	}
	defer a.Close()
	b, err := Open(dir, "carol@mail.src.ru__dave@mail.dst.ru")
	if err != nil {
		t.Fatalf("Open(b): %v", err)
	}
	defer b.Close()

	if err := a.PutMapping("INBOX", 1, 10, "k-a"); err != nil {
		t.Fatalf("PutMapping(a): %v", err)
	}
	if _, _, ok := b.GetMapping("INBOX", 1); ok {
		t.Fatalf("namespace b видит запись namespace a")
	}

	// Файлы кэша разные.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	dbs := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".db" {
			dbs++
		}
	}
	if dbs < 2 {
		t.Fatalf("ожидалось >= 2 файлов кэша, получено %d", dbs)
	}
}

// TestDBName проверяет санитизацию namespace в имя файла.
func TestDBName(t *testing.T) {
	cases := map[string]string{
		"":                 "cache.db",
		"a@h1__b@h2":       "cache-a_h1__b_h2.db",
		"user@example.com": "cache-user_example_com.db",
		"///":              "cache.db",
		"keep-99_":         "cache-keep-99.db",
	}
	for in, want := range cases {
		if got := DBName(in); got != want {
			t.Fatalf("DBName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestConcurrentWriters имитирует параллельные сессии: несколько
// независимых соединений пишут в одну базу. В WAL + busy_timeout записи
// сериализуются SQLite, потерь и ошибок быть не должно.
func TestConcurrentWriters(t *testing.T) {
	dir, err := os.MkdirTemp("", "imapsync_cache_conc_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	const writers = 8
	const perWriter = 25

	caches := make([]*Cache, writers)
	for i := range caches {
		c, err := Open(dir, "")
		if err != nil {
			t.Fatalf("Open(%d): %v", i, err)
		}
		defer c.Close()
		caches[i] = c
	}

	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i, c := range caches {
		wg.Add(1)
		go func(i int, c *Cache) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if err := c.PutMapping("INBOX", uint32(i*1000+j), uint32(j), "key"); err != nil {
					errs[i] = err
					return
				}
			}
		}(i, c)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}

	// Все записи на месте.
	total := 0
	for i := range caches {
		for j := 0; j < perWriter; j++ {
			if _, _, ok := caches[i].GetMapping("INBOX", uint32(i*1000+j)); ok {
				total++
			}
		}
	}
	if total != writers*perWriter {
		t.Fatalf("записей %d, ожидалось %d", total, writers*perWriter)
	}
}
