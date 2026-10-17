// Package store 用 SQLite 保存采集源、运行进度和采集结果。
package store

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"gospider/internal/extract"
	"gospider/internal/job"

	_ "modernc.org/sqlite"
)

// Store 是一个 gospider.db。
type Store struct {
	db   *sql.DB
	Path string
}

// Source 是一条采集源：名字、模式，以及原始 YAML。
type Source struct {
	Name      string
	Mode      string
	Config    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Records   int
	LastJob   string
	LastPID   int
}

// Open 打开或创建数据库，并建好表。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, Path: path}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭数据库。
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		return err
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sources (
  name TEXT PRIMARY KEY,
  mode TEXT NOT NULL,
  config TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS jobs (
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
  output TEXT NOT NULL DEFAULT '',
  proxy TEXT NOT NULL DEFAULT ''
)`,
		`CREATE TABLE IF NOT EXISTS job_logs (
  id INTEGER PRIMARY KEY,
  job_id TEXT NOT NULL,
  message TEXT NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_job_logs_job ON job_logs(job_id, id)`,
		`CREATE TABLE IF NOT EXISTS records (
  id INTEGER PRIMARY KEY,
  source_name TEXT NOT NULL,
  job_id TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_records_source ON records(source_name, id)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return s.ensureJobProxy()
}

func (s *Store) ensureJobProxy() error {
	rows, err := s.db.Query(`PRAGMA table_info(jobs)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "proxy" {
			has = true
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil || has {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE jobs ADD COLUMN proxy TEXT NOT NULL DEFAULT ''`)
	return err
}

// UpsertSource 按名称写入采集源。已存在则更新配置。
func (s *Store) UpsertSource(name, mode, config string) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
INSERT INTO sources(name, mode, config, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET mode=excluded.mode, config=excluded.config, updated_at=excluded.updated_at
`, name, mode, config, now, now)
	return err
}

// Source 读取一条采集源。
func (s *Store) Source(name string) (Source, error) {
	row := s.db.QueryRow(`SELECT name, mode, config, created_at, updated_at FROM sources WHERE name=?`, name)
	src, err := scanSource(row)
	if err == sql.ErrNoRows {
		return Source{}, fmt.Errorf("没有采集源 %s", name)
	}
	return src, err
}

// Sources 列出全部采集源，附带记录数和最近一次任务状态。
func (s *Store) Sources() ([]Source, error) {
	rows, err := s.db.Query(`
SELECT s.name, s.mode, s.config, s.created_at, s.updated_at,
  (SELECT COUNT(*) FROM records r WHERE r.source_name=s.name),
  COALESCE((SELECT status FROM jobs j WHERE j.source_name=s.name ORDER BY started_at DESC LIMIT 1), ''),
  COALESCE((SELECT pid FROM jobs j WHERE j.source_name=s.name ORDER BY started_at DESC LIMIT 1), 0)
FROM sources s
ORDER BY s.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var src Source
		var created, updated string
		if err := rows.Scan(&src.Name, &src.Mode, &src.Config, &created, &updated, &src.Records, &src.LastJob, &src.LastPID); err != nil {
			return nil, err
		}
		src.CreatedAt = parseTime(created)
		src.UpdatedAt = parseTime(updated)
		out = append(out, src)
	}
	return out, rows.Err()
}

// DeleteSource 删除采集源。purge 为真时连同记录、任务和日志一起删除。
func (s *Store) DeleteSource(name string, purge bool) error {
	if _, err := s.Source(name); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if purge {
		if _, err := tx.Exec(`DELETE FROM job_logs WHERE job_id IN (SELECT id FROM jobs WHERE source_name=?)`, name); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM jobs WHERE source_name=?`, name); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM records WHERE source_name=?`, name); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM sources WHERE name=?`, name); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveJob 写入或更新一次运行的进度。
func (s *Store) SaveJob(snap job.Snapshot) error {
	now := time.Now()
	snap.UpdatedAt = now
	if snap.StartedAt.IsZero() {
		snap.StartedAt = now
	}
	_, err := s.db.Exec(`
INSERT INTO jobs(id, source_name, pid, status, started_at, updated_at, total, done, success, failed, items, queued, current, last_error, output, proxy)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  status=excluded.status, updated_at=excluded.updated_at, total=excluded.total, done=excluded.done,
  success=excluded.success, failed=excluded.failed, items=excluded.items, queued=excluded.queued,
  current=excluded.current, last_error=excluded.last_error, output=excluded.output, pid=excluded.pid,
  proxy=excluded.proxy
`, snap.ID, snap.Name, snap.PID, snap.Status, snap.StartedAt.Format(time.RFC3339Nano), snap.UpdatedAt.Format(time.RFC3339Nano),
		snap.Total, snap.Done, snap.Success, snap.Failed, snap.Items, snap.Queued, snap.Current, snap.LastError, snap.Output, snap.Proxy)
	return err
}

// Jobs 返回全部运行记录，新的在前。
func (s *Store) Jobs() ([]job.Snapshot, error) {
	rows, err := s.db.Query(`SELECT id, source_name, pid, status, started_at, updated_at, total, done, success, failed, items, queued, current, last_error, output, proxy FROM jobs ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []job.Snapshot
	for rows.Next() {
		snap, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// ResolveJob 按编号或前缀找到唯一任务。
func (s *Store) ResolveJob(id string) (job.Snapshot, error) {
	all, err := s.Jobs()
	if err != nil {
		return job.Snapshot{}, err
	}
	var hits []job.Snapshot
	for _, snap := range all {
		if snap.ID == id || strings.HasPrefix(snap.ID, id) {
			hits = append(hits, snap)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return job.Snapshot{}, fmt.Errorf("找不到任务 %s", id)
	default:
		return job.Snapshot{}, fmt.Errorf("任务编号 %s 匹配到多个，请写得更完整", id)
	}
}

// AppendLog 追加一条错误日志。
func (s *Store) AppendLog(jobID, msg string) error {
	msg = strings.ReplaceAll(strings.TrimSpace(msg), "\n", " ")
	if msg == "" {
		return nil
	}
	line := time.Now().Format("15:04:05") + " " + msg
	_, err := s.db.Exec(`INSERT INTO job_logs(job_id, message) VALUES (?, ?)`, jobID, line)
	return err
}

// Logs 返回某个任务的错误日志。没有日志时 ok 为 false。
func (s *Store) Logs(jobID string) (string, bool, error) {
	rows, err := s.db.Query(`SELECT message FROM job_logs WHERE job_id=? ORDER BY id`, jobID)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	var b strings.Builder
	n := 0
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", false, err
		}
		b.WriteString(line)
		b.WriteByte('\n')
		n++
	}
	return b.String(), n > 0, rows.Err()
}

// AddRecord 保存一条采集结果。
func (s *Store) AddRecord(source, jobID string, item map[string]any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(item); err != nil {
		return err
	}
	body := strings.TrimRight(buf.String(), "\n")
	_, err := s.db.Exec(`INSERT INTO records(source_name, job_id, body, created_at) VALUES (?, ?, ?, ?)`,
		source, jobID, body, time.Now().Format(time.RFC3339Nano))
	return err
}

// RecentRecords 返回最新的若干条，旧的在前，便于直接阅读。
func (s *Store) RecentRecords(source string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT body FROM records WHERE source_name=? ORDER BY id DESC LIMIT ?`, source, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rev []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		rev = append(rev, body)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, nil
}

// CountRecords 返回某个采集源的结果条数。
func (s *Store) CountRecords(source string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM records WHERE source_name=?`, source).Scan(&n)
	return n, err
}

// ExportJSONL 把某个采集源的全部结果按采集顺序写出去。
func (s *Store) ExportJSONL(source string, w io.Writer) (int, error) {
	rows, err := s.db.Query(`SELECT body FROM records WHERE source_name=? ORDER BY id`, source)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return n, err
		}
		if _, err := io.WriteString(w, body+"\n"); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// ExportCSV 把结果写成 CSV。列按字段名排序，方便反复导出。
func (s *Store) ExportCSV(source string, w io.Writer) (int, error) {
	rows, err := s.db.Query(`SELECT body FROM records WHERE source_name=? ORDER BY id`, source)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var items []map[string]any
	var cols []string
	seen := map[string]struct{}{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return 0, err
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(body), &item); err != nil {
			return 0, err
		}
		items = append(items, item)
		for k := range item {
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			cols = append(cols, k)
		}
	}
	sort.Strings(cols)
	if err := rows.Err(); err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	if len(cols) > 0 {
		if err := cw.Write(cols); err != nil {
			return 0, err
		}
	}
	for _, item := range items {
		row := make([]string, len(cols))
		for i, c := range cols {
			row[i] = extract.Stringify(item[c])
		}
		if err := cw.Write(row); err != nil {
			return 0, err
		}
	}
	cw.Flush()
	return len(items), cw.Error()
}

// RecordSink 先把结果写入 SQLite，再按需写到文件。
type RecordSink struct {
	DB     *Store
	Source string
	JobID  string
	File   interface {
		Write(item map[string]any) error
	}
}

// Write 保存一条记录。
func (r *RecordSink) Write(item map[string]any) error {
	if err := r.DB.AddRecord(r.Source, r.JobID, item); err != nil {
		return err
	}
	if r.File != nil {
		return r.File.Write(item)
	}
	return nil
}

func scanSource(row *sql.Row) (Source, error) {
	var src Source
	var created, updated string
	if err := row.Scan(&src.Name, &src.Mode, &src.Config, &created, &updated); err != nil {
		return Source{}, err
	}
	src.CreatedAt = parseTime(created)
	src.UpdatedAt = parseTime(updated)
	return src, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanJob(row scanner) (job.Snapshot, error) {
	var snap job.Snapshot
	var started, updated string
	err := row.Scan(&snap.ID, &snap.Name, &snap.PID, &snap.Status, &started, &updated,
		&snap.Total, &snap.Done, &snap.Success, &snap.Failed, &snap.Items, &snap.Queued,
		&snap.Current, &snap.LastError, &snap.Output, &snap.Proxy)
	if err != nil {
		return job.Snapshot{}, err
	}
	snap.StartedAt = parseTime(started)
	snap.UpdatedAt = parseTime(updated)
	return snap, nil
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// DefaultPath 是当前目录下的数据库。可用环境变量 GOSPIDER_DB 改掉。
func DefaultPath() string {
	if p := os.Getenv("GOSPIDER_DB"); p != "" {
		return p
	}
	return "gospider.db"
}
