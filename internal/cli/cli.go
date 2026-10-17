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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"gospider/internal/config"
	"gospider/internal/engine"
	"gospider/internal/job"
	"gospider/internal/output"
	"gospider/internal/store"
)

// Stdout 和 Stderr 可以在测试里换成缓冲区。
var (
	Stdout io.Writer = os.Stdout
	Stderr io.Writer = os.Stderr
)

const usageText = `gospider 把采集源和结果放在 SQLite 里。

用法:
  gospider source add -c 任务.yaml     登记一个采集源
  gospider source list                  查看采集源
  gospider run 名称                     按采集源开始采集
  gospider run -c 任务.yaml             登记并立刻采集
  gospider ps                           查看正在采集的进程
  gospider records 名称                 查看已采集的记录
  gospider export 名称 -o 文件.jsonl    把记录导出成文件

数据库默认是当前目录的 gospider.db，可用环境变量 GOSPIDER_DB 指定。
`

// Run 执行命令，返回进程退出码。
func Run(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(Stdout, usageText)
		return 0
	}
	switch args[0] {
	case "source":
		return sourceCmd(args[1:])
	case "run":
		return run(args[1:])
	case "ps", "status":
		return ps(args[1:])
	case "stop":
		return stop(args[1:])
	case "log":
		return logCmd(args[1:])
	case "records":
		return recordsCmd(args[1:])
	case "export":
		return exportCmd(args[1:])
	default:
		fmt.Fprintf(Stderr, "未知命令 %s\n\n", args[0])
		fmt.Fprint(Stderr, usageText)
		return 2
	}
}

func openDB() (*store.Store, error) {
	return store.Open(store.DefaultPath())
}

// reorder 让名称和开关谁先写都可以，例如 records books -n 5。
func reorder(args []string, valued map[string]bool) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			if _, _, ok := strings.Cut(a, "="); ok {
				continue
			}
			if valued[strings.TrimLeft(a, "-")] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(Stderr)
	cfgPath := fs.String("c", "", "从这份 YAML 登记采集源并立刻运行")
	quiet := fs.Bool("q", false, "不刷新画面，隔几秒打一行")
	maxPages := fs.Int("max-pages", -1, "这次最多抓多少页（含入口），1 表示不翻页")
	fs.Usage = func() {
		fmt.Fprint(Stderr, "用法: gospider run 名称 [-max-pages 1] [-q]\n      gospider run -c 任务.yaml\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(reorder(args, map[string]bool{"c": true, "max-pages": true})); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	db, err := openDB()
	if err != nil {
		fmt.Fprintf(Stderr, "打开数据库失败: %v\n", err)
		return 1
	}
	defer db.Close()

	var raw []byte
	var cfg *config.Config
	switch {
	case *cfgPath != "" && fs.NArg() > 0:
		fmt.Fprintln(Stderr, "请只指定采集源名称，或只用 -c")
		return 2
	case *cfgPath != "":
		raw, err = os.ReadFile(*cfgPath)
		if err != nil {
			fmt.Fprintf(Stderr, "读取配置: %v\n", err)
			return 2
		}
		cfg, err = config.ParseNamed(raw, strings.TrimSuffix(filepath.Base(*cfgPath), filepath.Ext(*cfgPath)))
		if err != nil {
			fmt.Fprintf(Stderr, "配置错误: %v\n", err)
			return 2
		}
		if err := db.UpsertSource(cfg.Name, cfg.Mode, string(raw)); err != nil {
			fmt.Fprintf(Stderr, "保存采集源失败: %v\n", err)
			return 1
		}
		fmt.Fprintf(Stderr, "采集源 %s\n", cfg.Name)
	case fs.NArg() == 1:
		src, err := db.Source(fs.Arg(0))
		if err != nil {
			fmt.Fprintln(Stderr, err.Error())
			return 1
		}
		cfg, err = config.ParseNamed([]byte(src.Config), src.Name)
		if err != nil {
			fmt.Fprintf(Stderr, "采集源配置已损坏: %v\n", err)
			return 1
		}
	default:
		fs.Usage()
		return 2
	}
	if *maxPages >= 0 {
		cfg.MaxPages = *maxPages
	}

	var fileOut *output.Writer
	if cfg.Output.Format != "sqlite" {
		fileOut, err = output.New(cfg.Output.Format, cfg.Output.Path, cfg.Columns())
		if err != nil {
			fmt.Fprintf(Stderr, "输出错误: %v\n", err)
			return 1
		}
		defer fileOut.Close()
	}

	id := job.NewID()
	sink := &store.RecordSink{DB: db, Source: cfg.Name, JobID: id}
	if fileOut != nil {
		sink.File = fileOut
	}
	eng, err := engine.New(cfg, sink)
	if err != nil {
		fmt.Fprintf(Stderr, "启动失败: %v\n", err)
		return 1
	}
	eng.Log = func(msg string) {
		_ = db.AppendLog(id, msg)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	view := &View{w: Stderr, tty: isTTY(Stderr), quiet: *quiet}
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
			Proxy:     st.Proxy,
		}
		if s.Output == "" {
			s.Output = db.Path
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
		_ = db.SaveJob(s)
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
	fs.Usage = func() { fmt.Fprintln(Stderr, "用法: gospider ps [-a]") }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	db, err := openDB()
	if err != nil {
		fmt.Fprintf(Stderr, "打开数据库失败: %v\n", err)
		return 1
	}
	defer db.Close()
	list, err := db.Jobs()
	if err != nil {
		fmt.Fprintf(Stderr, "读取进度失败: %v\n", err)
		return 1
	}
	var rows []job.Snapshot
	for _, s := range list {
		s = markLost(db, s)
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
	printJobs(rows)
	return 0
}

func printJobs(rows []job.Snapshot) {
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
	db, err := openDB()
	if err != nil {
		fmt.Fprintf(Stderr, "打开数据库失败: %v\n", err)
		return 1
	}
	defer db.Close()
	s, err := db.ResolveJob(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(Stderr, err.Error())
		return 1
	}
	s = markLost(db, s)
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
	db, err := openDB()
	if err != nil {
		fmt.Fprintf(Stderr, "打开数据库失败: %v\n", err)
		return 1
	}
	defer db.Close()
	s, err := db.ResolveJob(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(Stderr, err.Error())
		return 1
	}
	text, ok, err := db.Logs(s.ID)
	if err != nil {
		fmt.Fprintf(Stderr, "读取日志失败: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintln(Stdout, "暂无错误日志")
		return 0
	}
	fmt.Fprint(Stdout, text)
	return 0
}

func markLost(db *store.Store, s job.Snapshot) job.Snapshot {
	if s.Status == "running" && !job.Alive(s.PID) {
		s.Status = "lost"
		if s.LastError == "" {
			s.LastError = "进程已不在"
		}
		_ = db.SaveJob(s)
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
