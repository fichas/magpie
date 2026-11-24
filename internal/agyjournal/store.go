package agyjournal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/yetone/magpie/internal/sqlitedb"
)

// Store keeps one versioned record per key. A record's generation starts
// at 1 when it is first written and goes up by one with each swap; an
// expired record reads as none (generation 0) and may be claimed again.
type Store interface {
	// Load is the record under key and its generation, nil and 0 for none.
	Load(ctx context.Context, key string) ([]byte, int64, error)
	// CompareAndSwap writes payload under key, kept for ttl, only while the
	// key's generation is gen (0: no live record), and says whether it did.
	CompareAndSwap(ctx context.Context, key string, gen int64, payload []byte, ttl time.Duration) (bool, error)
	// DeleteIfGeneration removes key only while its generation is gen.
	DeleteIfGeneration(ctx context.Context, key string, gen int64) (bool, error)
}

// SQLite is a Store in one SQLite file, shared by every magpie process
// that opens the same path: each swap is one statement, so two processes
// racing for one key never both win it.
type SQLite struct {
	db  *sql.DB
	now func() time.Time
}

// OpenSQLite opens (or makes) the journal at path, 0600.
func OpenSQLite(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	slashed := filepath.ToSlash(path)
	if len(slashed) >= 2 && slashed[1] == ':' {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed, RawQuery: "_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS replies (
		key TEXT PRIMARY KEY,
		gen INTEGER NOT NULL,
		payload BLOB NOT NULL,
		expires INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS replies_expires ON replies(expires);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	os.Chmod(path, 0o600)
	return &SQLite{db: db, now: time.Now}, nil
}

// Close lets go of the file.
func (s *SQLite) Close() error { return s.db.Close() }

func (s *SQLite) Load(ctx context.Context, key string) ([]byte, int64, error) {
	var payload []byte
	var gen int64
	err := s.db.QueryRowContext(ctx, `SELECT payload, gen FROM replies WHERE key = ? AND expires > ?`, key, s.now().UnixMilli()).Scan(&payload, &gen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return payload, gen, nil
}

func (s *SQLite) CompareAndSwap(ctx context.Context, key string, gen int64, payload []byte, ttl time.Duration) (bool, error) {
	now := s.now()
	var res sql.Result
	var err error
	if gen == 0 {
		// a claim: a new key, or one whose record has expired
		res, err = s.db.ExecContext(ctx, `INSERT INTO replies (key, gen, payload, expires) VALUES (?, 1, ?, ?)
			ON CONFLICT(key) DO UPDATE SET gen = 1, payload = excluded.payload, expires = excluded.expires
			WHERE replies.expires <= ?`, key, payload, now.Add(ttl).UnixMilli(), now.UnixMilli())
	} else {
		res, err = s.db.ExecContext(ctx, `UPDATE replies SET gen = gen + 1, payload = ?, expires = ?
			WHERE key = ? AND gen = ? AND expires > ?`, payload, now.Add(ttl).UnixMilli(), key, gen, now.UnixMilli())
	}
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *SQLite) DeleteIfGeneration(ctx context.Context, key string, gen int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM replies WHERE key = ? AND gen = ?`, key, gen)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Prune drops the records expired before now.
func (s *SQLite) Prune(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM replies WHERE expires <= ?`, s.now().UnixMilli())
	return err
}

var (
	sharedMu   sync.Mutex
	shared     *SQLite
	sharedPath string
)

// Shared is the journal at path, opened once per process and path; an
// open that failed is tried again on the next call.
func Shared(path string) (*SQLite, error) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if shared != nil && sharedPath == path {
		return shared, nil
	}
	s, err := OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	if shared != nil {
		shared.Close()
	}
	shared, sharedPath = s, path
	go s.Prune(context.Background())
	return s, nil
}
