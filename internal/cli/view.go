package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"gospider/internal/job"
)

// View 在终端里刷新进度。不是终端时改成隔几秒打一行。
type View struct {
	w     io.Writer
	tty   bool
	quiet bool
	lines int
	last  time.Time
}

func (v *View) Update(s job.Snapshot) {
	if v == nil {
		return
	}
	if !v.tty || v.quiet {
		if !v.last.IsZero() && time.Since(v.last) < 2*time.Second {
			return
		}
		v.last = time.Now()
		fmt.Fprintln(v.w, oneLine(s))
		return
	}
	text := live(s)
	if v.lines > 0 {
		fmt.Fprintf(v.w, "\033[%dA\033[J", v.lines)
	}
	fmt.Fprint(v.w, text)
	v.lines = strings.Count(text, "\n")
}

func (v *View) Finish(s job.Snapshot) {
	if v == nil {
		return
	}
	if v.tty && !v.quiet && v.lines > 0 {
		fmt.Fprintf(v.w, "\033[%dA\033[J", v.lines)
		v.lines = 0
	}
	fmt.Fprint(v.w, summary(s))
}

func live(s job.Snapshot) string {
	var b strings.Builder
	el := job.Elapsed(s, time.Now())
	fmt.Fprintf(&b, "采集  %s    任务 %s    进程 %d\n", s.Name, s.ID, s.PID)
	fmt.Fprintf(&b, "状态  %s    耗时 %s    速度 %s\n", job.StatusText(s.Status, true), job.FormatDuration(el), job.Rate(s.Success+s.Failed, el))
	fmt.Fprintf(&b, "进度  %s\n", job.Progress(s))
	fmt.Fprintf(&b, "请求  成功 %d    失败 %d    条目 %d    队列 %d\n", s.Success, s.Failed, s.Items, s.Queued)
	if s.Proxy != "" {
		fmt.Fprintf(&b, "代理  %s\n", s.Proxy)
	}
	cur := s.Current
	if cur == "" {
		cur = "-"
	}
	fmt.Fprintf(&b, "当前  %s\n", cur)
	if s.LastError != "" {
		fmt.Fprintf(&b, "最近错误  %s\n", s.LastError)
	}
	b.WriteString("Ctrl+C 停止\n")
	return b.String()
}

func summary(s job.Snapshot) string {
	var b strings.Builder
	el := job.Elapsed(s, time.Now())
	fmt.Fprintf(&b, "任务  %s  %s    %s\n", s.ID, s.Name, job.StatusText(s.Status, job.Alive(s.PID)))
	fmt.Fprintf(&b, "进度  %s\n", job.Progress(s))
	fmt.Fprintf(&b, "请求  成功 %d    失败 %d    条目 %d\n", s.Success, s.Failed, s.Items)
	fmt.Fprintf(&b, "耗时  %s    速度 %s\n", job.FormatDuration(el), job.Rate(s.Success+s.Failed, el))
	if s.Proxy != "" {
		fmt.Fprintf(&b, "代理  %s\n", s.Proxy)
	}
	if s.Output != "" && s.Output != "-" {
		fmt.Fprintf(&b, "输出  %s\n", s.Output)
	}
	if s.LastError != "" {
		fmt.Fprintf(&b, "最近错误  %s\n", s.LastError)
		fmt.Fprintf(&b, "日志  gospider log %s\n", s.ID)
	}
	return b.String()
}

func oneLine(s job.Snapshot) string {
	line := fmt.Sprintf("%s  %s  %s  成功 %d  失败 %d  条目 %d",
		s.Name, job.StatusText(s.Status, true), job.Progress(s), s.Success, s.Failed, s.Items)
	if s.Proxy != "" {
		line += "  代理 " + s.Proxy
	}
	return line
}
