// Package cache реализует персистентный локальный кэш синхронизации на SQLite
// для опций --usecache и --useuid (аналог кэша imapsync).
//
// Кэш рассчитан на параллельные сессии: база открывается в режиме WAL
// (читатели не блокируют писателя) с busy_timeout (писатели ждут блокировку,
// а не падают с SQLITE_BUSY), а каждая пара аккаунтов получает отдельный
// файл по namespace — см. Open.
package cache

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// busyTimeoutMS — время ожидания блокировки записи (SQLite busy_timeout).
const busyTimeoutMS = 5000

// Cache хранит соответствие между сообщениями host1 и host2.
type Cache struct {
	mu sync.Mutex
	db *sql.DB
}

// Open открывает или создаёт базу SQLite в каталоге dir для namespace.
//
// namespace задаёт имя файла (cache-<namespace>.db): разные пары аккаунтов
// работают в разных файлах и не конкурируют за запись. Пустой namespace
// даёт общий файл cache.db (совместимость со старыми кэшами).
//
// Файл открывается в WAL + busy_timeout: несколько процессов (параллельные
// запуски) могут писать в одну базу, блокировки разрешаются ожиданием.
func Open(dir, namespace string) (*Cache, error) {
	if dir == "" {
		dir = ".imapsync_cache"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("создание каталога кэша %q: %w", dir, err)
	}
	dbPath := filepath.Join(dir, DBName(namespace))

	// WAL: читатели не мешают писателю; busy_timeout: писатели дожидаются
	// блокировки вместо SQLITE_BUSY; synchronous=NORMAL — быстрый WAL-журнал.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		dbPath, busyTimeoutMS)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("открытие кэша %q: %w", dbPath, err)
	}

	// В одном процессе запись идёт по одному соединению (WAL допускает
	// одного писателя за раз); межпроцессная конкурентность — за WAL.
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

// DBName возвращает имя файла кэша для namespace (без каталога).
// Пустой namespace — общий файл cache.db.
func DBName(namespace string) string {
	if namespace == "" {
		return "cache.db"
	}
	var b strings.Builder
	for _, r := range namespace {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		return "cache.db"
	}
	return "cache-" + name + ".db"
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

// JournalMode возвращает текущий режим журнала базы (wal/delete и т.п.) —
// для проверки того, что база открыта в WAL.
func (c *Cache) JournalMode() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db == nil {
		return "", fmt.Errorf("кэш закрыт")
	}
	var mode string
	if err := c.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return "", err
	}
	return mode, nil
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
