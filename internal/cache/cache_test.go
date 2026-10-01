package cache

import (
	"os"
	"testing"
)

func TestCacheSqlite(t *testing.T) {
	dir, err := os.MkdirTemp("", "imapsync_cache_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	c, err := Open(dir)
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
