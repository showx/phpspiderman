// Package cli 是 gospider 的命令行入口。
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"gospider/internal/config"
	"gospider/internal/engine"
	"gospider/internal/job"
	"gospider/internal/output"
)

// Stdout 和 Stderr 可以在测试里换成缓冲区。
var (
	Stdout io.Writer = os.Stdout
	Stderr io.Writer = os.Stderr
)

const usageText = `gospider 是一个直接上手的采集工具。

用法:
  gospider run -c 任务.yaml          开始采集，终端里看进度
  gospider run -c 任务.yaml -max-pages 1
  gospider ps                         查看正在采集的进程
  gospider ps -a                      连同已结束的任务一起看
  gospider stop 任务编号              停止一个任务
  gospider log 任务编号               查看这个任务的错误日志

试跑网页示例:
  go run ./cmd/gospider run -c examples/site.yaml -max-pages 1

进度写在当前目录的 .gospider/ 里，所以要在同一个目录下执行 ps。
`

// Run 执行命令，返回进程退出码。
func Run(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(Stdout, usageText)
		return 0
	}
	switch args[0] {
	case "run":
		return run(args[1:])
	case "ps", "status":
		return ps(args[1:])
	case "stop":
		return stop(args[1:])
	case "log":
		return logCmd(args[1:])
	default:
		fmt.Fprintf(Stderr, "未知命令 %s\n\n", args[0])
		fmt.Fprint(Stderr, usageText)
		return 2
	}
}

func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(Stderr)
	cfgPath := fs.String("c", "", "任务配置文件")
	quiet := fs.Bool("q", false, "不刷新画面，隔几秒打一行")
	maxPages := fs.Int("max-pages", -1, "这次最多抓多少页（含入口），1 表示不翻页")
	fs.Usage = func() {
		fmt.Fprint(Stderr, "用法: gospider run -c 任务.yaml [-max-pages 1] [-q]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *cfgPath == "" {
		fmt.Fprintln(Stderr, "请用 -c 指定配置文件")
		fs.Usage()
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(Stderr, "配置错误: %v\n", err)
		return 2
	}
	if *maxPages >= 0 {
		cfg.MaxPages = *maxPages
	}
	out, err := output.New(cfg.Output.Format, cfg.Output.Path, cfg.Columns())
	if err != nil {
		fmt.Fprintf(Stderr, "输出错误: %v\n", err)
		return 1
	}
	defer out.Close()

	eng, err := engine.New(cfg, out)
	if err != nil {
		fmt.Fprintf(Stderr, "启动失败: %v\n", err)
		return 1
	}
	id := job.NewID()
	eng.Log = func(msg string) {
		_ = job.AppendLog(id, msg)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	progress := Stderr
	view := &View{w: progress, tty: isTTY(progress), quiet: *quiet}
	var saveMu sync.Mutex
	snap := func(status string) job.Snapshot {
		st := eng.Stats()
		s := job.Snapshot{
			ID:        id,
			Name:      cfg.Name,
			PID:       os.Getpid(),
			Status:    status,
			StartedAt: st.Started,
			Total:     st.Total,
			Done:      st.Done,
			Success:   st.Success,
			Failed:    st.Failed,
			Items:     st.Items,
			Queued:    st.Queued,
			Current:   st.Current,
			LastError: st.LastError,
			Output:    cfg.Output.Path,
		}
		if s.StartedAt.IsZero() {
			s.StartedAt = time.Now()
		}
		return s
	}
	save := func(status string) job.Snapshot {
		saveMu.Lock()
		defer saveMu.Unlock()
		s := snap(status)
		_ = job.Save(s)
		return s
	}

	save("running")
	stopTick := make(chan struct{})
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopTick:
				return
			case <-ticker.C:
				view.Update(save("running"))
			}
		}
	}()

	err = eng.Run(ctx)
	close(stopTick)
	<-tickDone

	status := "done"
	code := 0
	if err != nil && ctx.Err() != nil {
		status = "stopped"
	} else if err != nil {
		status = "failed"
		code = 1
	}
	final := save(status)
	view.Finish(final)
	if status == "failed" {
		fmt.Fprintf(Stderr, "采集失败: %v\n", err)
	}
	return code
}

func ps(args []string) int {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	fs.SetOutput(Stderr)
	all := fs.Bool("a", false, "包含已结束的任务")
	fs.Usage = func() {
		fmt.Fprintln(Stderr, "用法: gospider ps [-a]")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	list, err := job.List()
	if err != nil {
		fmt.Fprintf(Stderr, "读取进度失败: %v\n", err)
		return 1
	}
	var rows []job.Snapshot
	for _, s := range list {
		s = markLost(s)
		if !*all && s.Status != "running" {
			continue
		}
		rows = append(rows, s)
	}
	sort.Slice(rows, func(i, j int) bool {
		ri := rows[i].Status == "running"
		rj := rows[j].Status == "running"
		if ri != rj {
			return ri
		}
		return rows[i].StartedAt.After(rows[j].StartedAt)
	})
	if len(rows) == 0 {
		if *all {
			fmt.Fprintln(Stdout, "还没有采集任务")
		} else {
			fmt.Fprintln(Stdout, "当前没有正在采集的任务。用 ps -a 查看历史。")
		}
		return 0
	}
	limit := len(rows)
	if limit > 40 {
		limit = 40
	}
	fmt.Fprintln(Stdout, strings.Join([]string{
		pad("任务", 10), pad("名称", 16), pad("状态", 8), pad("PID", 8),
		pad("进度", 22), pad("成功", 6), pad("失败", 6), pad("条目", 8), pad("速度", 10), "耗时",
	}, " "))
	now := time.Now()
	for _, s := range rows[:limit] {
		el := job.Elapsed(s, now)
		fmt.Fprintln(Stdout, strings.Join([]string{
			pad(s.ID, 10),
			pad(trimRunes(s.Name, 16), 16),
			pad(job.StatusText(s.Status, s.Status == "running" && job.Alive(s.PID)), 8),
			pad(fmt.Sprintf("%d", s.PID), 8),
			pad(job.Progress(s), 22),
			pad(fmt.Sprintf("%d", s.Success), 6),
			pad(fmt.Sprintf("%d", s.Failed), 6),
			pad(fmt.Sprintf("%d", s.Items), 8),
			pad(job.Rate(s.Success+s.Failed, el), 10),
			job.FormatDuration(el),
		}, " "))
	}
	if len(rows) > limit {
		fmt.Fprintf(Stdout, "还有 %d 条更早的任务没有列出\n", len(rows)-limit)
	}
	return 0
}

func stop(args []string) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(Stderr)
	fs.Usage = func() { fmt.Fprintln(Stderr, "用法: gospider stop 任务编号") }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	s, err := job.Resolve(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(Stderr, err.Error())
		return 1
	}
	s = markLost(s)
	if s.Status != "running" || !job.Alive(s.PID) {
		fmt.Fprintf(Stdout, "任务 %s 没有在运行（%s）\n", s.ID, job.StatusText(s.Status, false))
		return 0
	}
	if err := syscall.Kill(s.PID, syscall.SIGTERM); err != nil {
		fmt.Fprintf(Stderr, "停止失败: %v\n", err)
		return 1
	}
	fmt.Fprintf(Stdout, "已通知任务 %s 停止\n", s.ID)
	return 0
}

func logCmd(args []string) int {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	fs.SetOutput(Stderr)
	fs.Usage = func() { fmt.Fprintln(Stderr, "用法: gospider log 任务编号") }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	s, err := job.Resolve(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(Stderr, err.Error())
		return 1
	}
	b, err := os.ReadFile(job.LogPath(s.ID))
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(Stdout, "暂无错误日志")
			return 0
		}
		fmt.Fprintf(Stderr, "读取日志失败: %v\n", err)
		return 1
	}
	fmt.Fprint(Stdout, string(b))
	return 0
}

func markLost(s job.Snapshot) job.Snapshot {
	if s.Status == "running" && !job.Alive(s.PID) {
		s.Status = "lost"
		if s.LastError == "" {
			s.LastError = "进程已不在"
		}
		_ = job.Save(s)
	}
	return s
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func pad(s string, n int) string {
	w := displayWidth(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		if r > 0x2E80 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func trimRunes(s string, n int) string {
	if displayWidth(s) <= n {
		return s
	}
	out := make([]rune, 0, n)
	w := 0
	for _, r := range s {
		rw := 1
		if r > 0x2E80 {
			rw = 2
		}
		if w+rw > n-1 {
			break
		}
		out = append(out, r)
		w += rw
	}
	return string(out) + "…"
}
