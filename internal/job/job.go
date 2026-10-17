// Package job 描述一次采集运行的进度。进度和日志存在 SQLite 里。
package job

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	Proxy     string    `json:"proxy"`
}

// NewID 生成任务编号。
func NewID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

// Alive 判断进程是否还在。
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
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
