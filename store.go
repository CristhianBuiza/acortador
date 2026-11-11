package main

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("link no encontrado")
	ErrExists   = errors.New("el código ya existe")
)

type Link struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
	Clicks    int64     `json:"clicks"`
}

type Store struct {
	db *sql.DB
}

func openStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// SQLite admite un único escritor; una sola conexión evita SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	const schema = `
CREATE TABLE IF NOT EXISTS links (
	code       TEXT PRIMARY KEY,
	url        TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	clicks     INTEGER NOT NULL DEFAULT 0
);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Create(ctx context.Context, code, url string) (Link, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO links (code, url, created_at) VALUES (?, ?, ?) ON CONFLICT(code) DO NOTHING`,
		code, url, now.Unix())
	if err != nil {
		return Link{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Link{}, err
	}
	if n == 0 {
		return Link{}, ErrExists
	}
	return Link{Code: code, URL: url, CreatedAt: now.Truncate(time.Second)}, nil
}

// Resolve devuelve la URL destino y suma un clic.
func (s *Store) Resolve(ctx context.Context, code string) (string, error) {
	var url string
	err := s.db.QueryRowContext(ctx,
		`UPDATE links SET clicks = clicks + 1 WHERE code = ? RETURNING url`, code).Scan(&url)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return url, err
}

func (s *Store) List(ctx context.Context, limit int) ([]Link, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT code, url, created_at, clicks FROM links ORDER BY created_at DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	links := []Link{}
	for rows.Next() {
		var l Link
		var created int64
		if err := rows.Scan(&l.Code, &l.URL, &created, &l.Clicks); err != nil {
			return nil, err
		}
		l.CreatedAt = time.Unix(created, 0).UTC()
		links = append(links, l)
	}
	return links, rows.Err()
}

func (s *Store) Delete(ctx context.Context, code string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM links WHERE code = ?`, code)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
