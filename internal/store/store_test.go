package store

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"gospider/internal/job"
)

func TestSourceJobAndRecords(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertSource("books", "site", "name: books\nmode: site\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSource("books", "site", "name: books\nmode: site\nworkers: 2\n"); err != nil {
		t.Fatal(err)
	}
	src, err := s.Source("books")
	if err != nil || !bytes.Contains([]byte(src.Config), []byte("workers: 2")) {
		t.Fatalf("%+v %v", src, err)
	}
	list, err := s.Sources()
	if err != nil || len(list) != 1 || list[0].Records != 0 {
		t.Fatalf("%v %v", list, err)
	}

	snap := job.Snapshot{
		ID: "abc12345", Name: "books", PID: 1, Status: "running",
		StartedAt: time.Now().Add(-time.Second),
		Proxy:     "http://1.2.3.4:8080",
	}
	if err := s.SaveJob(snap); err != nil {
		t.Fatal(err)
	}
	snap.Status = "done"
	snap.Items = 1
	if err := s.SaveJob(snap); err != nil {
		t.Fatal(err)
	}
	got, err := s.ResolveJob("abc")
	if err != nil || got.Status != "done" || got.Items != 1 || got.Proxy != "http://1.2.3.4:8080" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.AppendLog("abc12345", "超时一次"); err != nil {
		t.Fatal(err)
	}
	logs, ok, err := s.Logs("abc12345")
	if err != nil || !ok || !bytes.Contains([]byte(logs), []byte("超时一次")) {
		t.Fatalf("%q %v %v", logs, ok, err)
	}

	if err := s.AddRecord("books", "abc12345", map[string]any{"title": "甲", "price": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRecord("books", "abc12345", map[string]any{"title": "乙", "price": "2"}); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountRecords("books")
	if err != nil || n != 2 {
		t.Fatalf("count %d %v", n, err)
	}
	recent, err := s.RecentRecords("books", 1)
	if err != nil || len(recent) != 1 || !bytes.Contains([]byte(recent[0]), []byte("乙")) {
		t.Fatalf("%v %v", recent, err)
	}
	var buf bytes.Buffer
	if n, err = s.ExportJSONL("books", &buf); err != nil || n != 2 || !bytes.Contains(buf.Bytes(), []byte("甲")) {
		t.Fatalf("export %d %v %s", n, err, buf.String())
	}

	if err := s.DeleteSource("books", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Source("books"); err == nil {
		t.Fatal("source should be gone")
	}
	if n, err = s.CountRecords("books"); err != nil || n != 2 {
		t.Fatalf("records kept %d %v", n, err)
	}
	if err := s.UpsertSource("books", "site", "name: books\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSource("books", true); err != nil {
		t.Fatal(err)
	}
	if n, err = s.CountRecords("books"); err != nil || n != 0 {
		t.Fatalf("purged %d %v", n, err)
	}
}

func TestOldJobsGainProxyColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE jobs (
  id TEXT PRIMARY KEY,
  source_name TEXT NOT NULL,
  pid INTEGER NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  total INTEGER NOT NULL DEFAULT 0,
  done INTEGER NOT NULL DEFAULT 0,
  success INTEGER NOT NULL DEFAULT 0,
  failed INTEGER NOT NULL DEFAULT 0,
  items INTEGER NOT NULL DEFAULT 0,
  queued INTEGER NOT NULL DEFAULT 0,
  current TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  output TEXT NOT NULL DEFAULT ''
)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO jobs(id, source_name, pid, status, started_at, updated_at) VALUES ('old1', 'books', 1, 'done', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	got, err := s.ResolveJob("old1")
	if err != nil || got.Status != "done" || got.Proxy != "" {
		t.Fatalf("%+v %v", got, err)
	}
}

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
