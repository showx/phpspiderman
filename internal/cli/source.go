package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gospider/internal/config"
	"gospider/internal/job"
)

func sourceCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(Stderr, `用法:
  gospider source add -c 任务.yaml
  gospider source list
  gospider source show 名称
  gospider source rm 名称 [-purge]
`)
		return 2
	}
	switch args[0] {
	case "add":
		return sourceAdd(args[1:])
	case "list", "ls":
		return sourceList(args[1:])
	case "show":
		return sourceShow(args[1:])
	case "rm", "remove":
		return sourceRemove(args[1:])
	default:
		fmt.Fprintf(Stderr, "未知子命令 source %s\n", args[0])
		return 2
	}
}

func sourceAdd(args []string) int {
	fs := flag.NewFlagSet("source add", flag.ContinueOnError)
	fs.SetOutput(Stderr)
	cfgPath := fs.String("c", "", "采集源配置文件")
	fs.Usage = func() { fmt.Fprintln(Stderr, "用法: gospider source add -c 任务.yaml") }
	if err := fs.Parse(reorder(args, map[string]bool{"c": true})); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *cfgPath == "" {
		fs.Usage()
		return 2
	}
	raw, err := os.ReadFile(*cfgPath)
	if err != nil {
		fmt.Fprintf(Stderr, "读取配置: %v\n", err)
		return 2
	}
	cfg, err := config.ParseNamed(raw, strings.TrimSuffix(filepath.Base(*cfgPath), filepath.Ext(*cfgPath)))
	if err != nil {
		fmt.Fprintf(Stderr, "配置错误: %v\n", err)
		return 2
	}
	db, err := openDB()
	if err != nil {
		fmt.Fprintf(Stderr, "打开数据库失败: %v\n", err)
		return 1
	}
	defer db.Close()
	if err := db.UpsertSource(cfg.Name, cfg.Mode, string(raw)); err != nil {
		fmt.Fprintf(Stderr, "保存采集源失败: %v\n", err)
		return 1
	}
	fmt.Fprintf(Stdout, "已登记采集源 %s（%s）\n", cfg.Name, cfg.Mode)
	return 0
}

func sourceList(args []string) int {
	fs := flag.NewFlagSet("source list", flag.ContinueOnError)
	fs.SetOutput(Stderr)
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
	list, err := db.Sources()
	if err != nil {
		fmt.Fprintf(Stderr, "读取采集源失败: %v\n", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Fprintln(Stdout, "还没有采集源。用 source add -c 任务.yaml 登记。")
		return 0
	}
	fmt.Fprintln(Stdout, strings.Join([]string{
		pad("名称", 16), pad("模式", 8), pad("记录", 8), pad("最近状态", 10), "更新",
	}, " "))
	for _, src := range list {
		status := "-"
		if src.LastJob != "" {
			status = job.StatusText(src.LastJob, src.LastJob == "running" && job.Alive(src.LastPID))
		}
		fmt.Fprintln(Stdout, strings.Join([]string{
			pad(trimRunes(src.Name, 16), 16),
			pad(src.Mode, 8),
			pad(fmt.Sprintf("%d", src.Records), 8),
			pad(status, 10),
			src.UpdatedAt.Format("2006-01-02 15:04"),
		}, " "))
	}
	return 0
}

func sourceShow(args []string) int {
	fs := flag.NewFlagSet("source show", flag.ContinueOnError)
	fs.SetOutput(Stderr)
	fs.Usage = func() { fmt.Fprintln(Stderr, "用法: gospider source show 名称") }
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
	src, err := db.Source(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(Stderr, err.Error())
		return 1
	}
	fmt.Fprint(Stdout, src.Config)
	if !strings.HasSuffix(src.Config, "\n") {
		fmt.Fprintln(Stdout)
	}
	return 0
}

func sourceRemove(args []string) int {
	fs := flag.NewFlagSet("source rm", flag.ContinueOnError)
	fs.SetOutput(Stderr)
	purge := fs.Bool("purge", false, "同时删除该采集源的记录和任务")
	fs.Usage = func() { fmt.Fprintln(Stderr, "用法: gospider source rm 名称 [-purge]") }
	if err := fs.Parse(reorder(args, nil)); err != nil {
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
	name := fs.Arg(0)
	if err := db.DeleteSource(name, *purge); err != nil {
		fmt.Fprintln(Stderr, err.Error())
		return 1
	}
	if *purge {
		fmt.Fprintf(Stdout, "已删除采集源 %s 及其记录\n", name)
	} else {
		fmt.Fprintf(Stdout, "已删除采集源 %s（记录仍保留，可用 -purge 一并删除）\n", name)
	}
	return 0
}

func recordsCmd(args []string) int {
	fs := flag.NewFlagSet("records", flag.ContinueOnError)
	fs.SetOutput(Stderr)
	n := fs.Int("n", 20, "最多显示多少条")
	fs.Usage = func() { fmt.Fprintln(Stderr, "用法: gospider records 名称 [-n 20]") }
	if err := fs.Parse(reorder(args, map[string]bool{"n": true})); err != nil {
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
	name := fs.Arg(0)
	total, err := db.CountRecords(name)
	if err != nil {
		fmt.Fprintf(Stderr, "读取记录失败: %v\n", err)
		return 1
	}
	rows, err := db.RecentRecords(name, *n)
	if err != nil {
		fmt.Fprintf(Stderr, "读取记录失败: %v\n", err)
		return 1
	}
	if len(rows) == 0 {
		fmt.Fprintf(Stdout, "采集源 %s 还没有记录\n", name)
		return 0
	}
	for _, row := range rows {
		fmt.Fprintln(Stdout, row)
	}
	fmt.Fprintf(Stderr, "显示 %d / %d 条。全部导出: gospider export %s -o 文件.jsonl\n", len(rows), total, name)
	return 0
}

func exportCmd(args []string) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(Stderr)
	outPath := fs.String("o", "", "导出文件")
	format := fs.String("f", "", "jsonl 或 csv，默认按扩展名判断")
	fs.Usage = func() { fmt.Fprintln(Stderr, "用法: gospider export 名称 -o 文件.jsonl") }
	if err := fs.Parse(reorder(args, map[string]bool{"o": true, "f": true})); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 || *outPath == "" {
		fs.Usage()
		return 2
	}
	kind := strings.ToLower(*format)
	if kind == "" {
		switch strings.ToLower(filepath.Ext(*outPath)) {
		case ".csv":
			kind = "csv"
		default:
			kind = "jsonl"
		}
	}
	if kind != "jsonl" && kind != "csv" {
		fmt.Fprintln(Stderr, "格式只能是 jsonl 或 csv")
		return 2
	}
	db, err := openDB()
	if err != nil {
		fmt.Fprintf(Stderr, "打开数据库失败: %v\n", err)
		return 1
	}
	defer db.Close()
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		fmt.Fprintf(Stderr, "创建目录失败: %v\n", err)
		return 1
	}
	f, err := os.Create(*outPath)
	if err != nil {
		fmt.Fprintf(Stderr, "创建文件失败: %v\n", err)
		return 1
	}
	defer f.Close()
	var n int
	if kind == "csv" {
		n, err = db.ExportCSV(fs.Arg(0), f)
	} else {
		n, err = db.ExportJSONL(fs.Arg(0), f)
	}
	if err != nil {
		fmt.Fprintf(Stderr, "导出失败: %v\n", err)
		return 1
	}
	fmt.Fprintf(Stdout, "已导出 %d 条到 %s\n", n, *outPath)
	return 0
}
