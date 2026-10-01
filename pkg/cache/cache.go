// Package cache реализует персистентный локальный кэш синхронизации на SQLite
// для опций --usecache и --useuid (аналог кэша imapsync).
package cache

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// Cache хранит соответствие между сообщениями host1 и host2.
type Cache struct {
	mu sync.Mutex
	db *sql.DB
}

// Open открывает или создаёт базу SQLite в указанном каталоге.
func Open(dir string) (*Cache, error) {
	if dir == "" {
		dir = ".imapsync_cache"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("создание каталога кэша %q: %w", dir, err)
	}
	dbPath := filepath.Join(dir, "cache.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("открытие кэша %q: %w", dbPath, err)
	}

	// Ограничиваем конкурентный доступ внутри SQLite
	db.SetMaxOpenConns(1)

	schema := `
	CREATE TABLE IF NOT EXISTS msg_map (
		folder TEXT NOT NULL,
		uid1 INTEGER NOT NULL,
		uid2 INTEGER NOT NULL,
		msg_key TEXT NOT NULL,
		PRIMARY KEY(folder, uid1)
	);
	CREATE INDEX IF NOT EXISTS idx_folder_uid2 ON msg_map(folder, uid2);
	CREATE INDEX IF NOT EXISTS idx_folder_key ON msg_map(folder, msg_key);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("инициализация схемы кэша: %w", err)
	}

	return &Cache{db: db}, nil
}

// Close закрывает базу данных кэша.
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db != nil {
		err := c.db.Close()
		c.db = nil
		return err
	}
	return nil
}

// GetMapping возвращает uid2 и msg_key для заданного (folder, uid1).
func (c *Cache) GetMapping(folder string, uid1 uint32) (uint32, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db == nil {
		return 0, "", false
	}
	var uid2 uint32
	var key string
	err := c.db.QueryRow("SELECT uid2, msg_key FROM msg_map WHERE folder = ? AND uid1 = ?", folder, uid1).Scan(&uid2, &key)
	if err != nil {
		return 0, "", false
	}
	return uid2, key, true
}

// PutMapping сохраняет соответствие (folder, uid1) -> (uid2, msg_key).
func (c *Cache) PutMapping(folder string, uid1, uid2 uint32, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db == nil {
		return nil
	}
	_, err := c.db.Exec(`
		INSERT INTO msg_map(folder, uid1, uid2, msg_key)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(folder, uid1) DO UPDATE SET uid2 = excluded.uid2, msg_key = excluded.msg_key
	`, folder, uid1, uid2, key)
	return err
}

// DeleteFolder очищает кэш для указанной папки.
func (c *Cache) DeleteFolder(folder string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db == nil {
		return nil
	}
	_, err := c.db.Exec("DELETE FROM msg_map WHERE folder = ?", folder)
	return err
}
