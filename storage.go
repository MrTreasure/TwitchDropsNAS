package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func openStore(dir string) (*Store, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, "state.sqlite")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS state (key TEXT PRIMARY KEY, value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS events (id INTEGER PRIMARY KEY, at TEXT, level TEXT, message TEXT);
 CREATE TABLE IF NOT EXISTS traffic (at REAL, route TEXT, bytes INTEGER);
 CREATE INDEX IF NOT EXISTS traffic_at ON traffic(at);`)
	if e != nil {
		db.Close()
		return nil, e
	}
	if e = os.Chmod(path, 0600); e != nil {
		db.Close()
		return nil, e
	}
	return &Store{db}, nil
}
func (s *Store) Get(key string, fallback any) any {
	var raw string
	e := s.db.QueryRow("SELECT value FROM state WHERE key=?", key).Scan(&raw)
	if e == sql.ErrNoRows {
		return clone(fallback)
	}
	must(e)
	var v any
	must(json.Unmarshal([]byte(raw), &v))
	return v
}
func (s *Store) Put(key string, v any) { s.PutMany(M{key: v}) }
func (s *Store) PutMany(values M) {
	tx, e := s.db.Begin()
	must(e)
	defer tx.Rollback()
	for k, v := range values {
		_, e = tx.Exec("INSERT INTO state VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, string(bytesJSON(v)))
		must(e)
	}
	must(tx.Commit())
}
func (s *Store) Event(message, level string) {
	_, e := s.db.Exec("INSERT INTO events(at,level,message) VALUES(?,?,?)", now(), level, message)
	must(e)
	_, e = s.db.Exec("DELETE FROM events WHERE id < (SELECT MAX(id)-500 FROM events)")
	must(e)
}
func (s *Store) Events() []any {
	rows, e := s.db.Query("SELECT id,at,level,message FROM events ORDER BY id DESC LIMIT 80")
	must(e)
	defer rows.Close()
	a := []any{}
	for rows.Next() {
		var id int64
		var at, level, message string
		must(rows.Scan(&id, &at, &level, &message))
		a = append(a, M{"id": id, "at": at, "level": level, "message": message})
	}
	must(rows.Err())
	return a
}
func (s *Store) Traffic(at float64, route string, count int64) {
	tx, e := s.db.Begin()
	must(e)
	defer tx.Rollback()
	_, e = tx.Exec("INSERT INTO traffic VALUES(?,?,?)", at, route, count)
	must(e)
	_, e = tx.Exec("DELETE FROM traffic WHERE at<?", at-172800)
	must(e)
	must(tx.Commit())
}
func (s *Store) TrafficSince(at float64, route string) int64 {
	q := "SELECT COALESCE(SUM(bytes),0) FROM traffic WHERE at>=?"
	args := []any{at}
	if route != "" {
		q += " AND route=?"
		args = append(args, route)
	}
	var n int64
	must(s.db.QueryRow(q, args...).Scan(&n))
	return n
}

type Credentials struct {
	path string
	book bool
	mu   sync.Mutex
}

func (c *Credentials) Read() (M, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, e := os.ReadFile(c.path)
	if os.IsNotExist(e) && !c.book {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var v M
	if c.book {
		e = yaml.Unmarshal(b, &v)
		if e == nil {
			v = obj(v["twitch_drops"])
		}
	} else {
		e = json.Unmarshal(b, &v)
	}
	if e != nil {
		return nil, errors.New("invalid private credential storage")
	}
	if v["account"] == nil {
		return nil, nil
	}
	return obj(v["account"]), nil
}
func atomicPrivate(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".twitch-drops-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return replacePrivate(f.Name(), path)
}
func (c *Credentials) Write(account M) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.book {
		return atomicPrivate(c.path, bytesJSON(M{"account": account}))
	}
	unlock, e := lockBook(filepath.Join(filepath.Dir(c.path), ".twitch-drops-book.lock"))
	if e != nil {
		return e
	}
	defer unlock()
	raw, e := os.ReadFile(c.path)
	if e != nil {
		return e
	}
	var doc yaml.Node
	if yaml.Unmarshal(raw, &doc) != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return errors.New("invalid password book")
	}
	var book M
	if yaml.Unmarshal(raw, &book) != nil {
		return errors.New("invalid password book")
	}
	namespace := obj(book["twitch_drops"])
	if account == nil {
		delete(namespace, "account")
	} else {
		namespace["account"] = account
	}
	book["twitch_drops"] = namespace
	block, e := yaml.Marshal(M{"twitch_drops": namespace})
	if e != nil {
		return e
	}
	// Replace only the top-level namespace span. Other suppliers' bytes stay intact.
	lines := bytes.SplitAfter(raw, []byte("\n"))
	offset := func(line int) int {
		n := 0
		for i := 0; i < line-1 && i < len(lines); i++ {
			n += len(lines[i])
		}
		return n
	}
	start, end := -1, len(raw)
	nodes := doc.Content[0].Content
	for i := 0; i < len(nodes); i += 2 {
		if nodes[i].Value == "twitch_drops" {
			start = offset(nodes[i].Line)
			if i+2 < len(nodes) {
				end = offset(nodes[i+2].Line)
			}
			break
		}
	}
	var updated []byte
	if start >= 0 {
		updated = append(updated, raw[:start]...)
		updated = append(updated, block...)
		updated = append(updated, raw[end:]...)
	} else {
		updated = append(updated, bytes.TrimRight(raw, "\n")...)
		updated = append(updated, '\n', '\n')
		updated = append(updated, block...)
	}
	var verify M
	if yaml.Unmarshal(updated, &verify) != nil || !bytes.Equal(bytesJSON(verify), bytesJSON(book)) {
		return errors.New("password book validation failed")
	}
	current, e := os.ReadFile(c.path)
	if e != nil || sha256.Sum256(current) != sha256.Sum256(raw) {
		return errors.New("password book changed concurrently")
	}
	return atomicPrivate(c.path, updated)
}
