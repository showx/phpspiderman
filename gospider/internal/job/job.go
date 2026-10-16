// Package job 把每个采集进程的进度记到本地，方便另一个命令查看。
package job

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Snapshot 是某个任务在某一刻的进度。
type Snapshot struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	PID       int       `json:"pid"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Total     int64     `json:"total"`
	Done      int64     `json:"done"`
	Success   int64     `json:"success"`
	Failed    int64     `json:"failed"`
	Items     int64     `json:"items"`
	Queued    int64     `json:"queued"`
	Current   string    `json:"current"`
	LastError string    `json:"last_error"`
	Output    string    `json:"output"`
}

// NewID 生成任务编号。
func NewID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

// Dir 是进度目录。可用环境变量 GOSPIDER_HOME 改到别处。
func Dir() string {
	if h := os.Getenv("GOSPIDER_HOME"); h != "" {
		return h
	}
	return ".gospider"
}

func jobsDir() string {
	return filepath.Join(Dir(), "jobs")
}

// Alive 判断进程是否还在。
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Save 原子写入进度文件。
func Save(s Snapshot) error {
	if err := safe(s.ID); err != nil {
		return err
	}
	if err := os.MkdirAll(jobsDir(), 0o755); err != nil {
		return err
	}
	s.UpdatedAt = time.Now()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := filepath.Join(jobsDir(), s.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load 读取一个任务。
func Load(id string) (Snapshot, error) {
	if err := safe(id); err != nil {
		return Snapshot{}, err
	}
	b, err := os.ReadFile(filepath.Join(jobsDir(), id+".json"))
	if err != nil {
		return Snapshot{}, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// List 返回全部任务。
func List() ([]Snapshot, error) {
	entries, err := os.ReadDir(jobsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Snapshot
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		s, err := Load(id)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// Resolve 按编号或前缀找到唯一任务。
func Resolve(id string) (Snapshot, error) {
	all, err := List()
	if err != nil {
		return Snapshot{}, err
	}
	var hits []Snapshot
	for _, s := range all {
		if s.ID == id || strings.HasPrefix(s.ID, id) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Snapshot{}, fmt.Errorf("找不到任务 %s", id)
	default:
		return Snapshot{}, fmt.Errorf("任务编号 %s 匹配到多个，请写得更完整", id)
	}
}

var logMu sync.Mutex

// AppendLog 追加一条错误日志。
func AppendLog(id, msg string) error {
	if err := safe(id); err != nil {
		return err
	}
	if err := os.MkdirAll(jobsDir(), 0o755); err != nil {
		return err
	}
	msg = strings.ReplaceAll(msg, "\n", " ")
	line := time.Now().Format("15:04:05") + " " + msg + "\n"
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.OpenFile(LogPath(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

// LogPath 是错误日志路径。
func LogPath(id string) string {
	return filepath.Join(jobsDir(), id+".log")
}

func safe(id string) error {
	if id == "" || id != filepath.Base(id) || strings.Contains(id, "..") {
		return fmt.Errorf("无效任务编号")
	}
	return nil
}

// StatusText 是给人看的状态。
func StatusText(status string, alive bool) string {
	switch status {
	case "running":
		if alive {
			return "运行中"
		}
		return "已中断"
	case "done":
		return "完成"
	case "stopped":
		return "已停止"
	case "failed":
		return "失败"
	case "lost":
		return "已中断"
	default:
		if status == "" {
			return "-"
		}
		return status
	}
}

// Elapsed 是已运行时间。结束的任务停在最后一次更新。
func Elapsed(s Snapshot, now time.Time) time.Duration {
	if s.StartedAt.IsZero() {
		return 0
	}
	end := now
	if s.Status != "running" && !s.UpdatedAt.IsZero() {
		end = s.UpdatedAt
	}
	d := end.Sub(s.StartedAt)
	if d < 0 {
		return 0
	}
	return d
}

// FormatDuration 把时长显示成分秒。
func FormatDuration(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// Rate 是请求速度。
func Rate(n int64, d time.Duration) string {
	if d < 500*time.Millisecond {
		return "-"
	}
	return fmt.Sprintf("%.1f/秒", float64(n)/d.Seconds())
}

// Progress 是进度文字。知道总页数时带百分比。
func Progress(s Snapshot) string {
	if s.Total > 0 {
		pct := s.Done * 100 / s.Total
		if pct > 100 {
			pct = 100
		}
		if pct < 0 {
			pct = 0
		}
		return fmt.Sprintf("%d/%d %d%%", s.Done, s.Total, pct)
	}
	if s.Queued > 0 {
		return fmt.Sprintf("%d 请求，队列 %d", s.Done, s.Queued)
	}
	return fmt.Sprintf("%d 请求", s.Done)
}
