// Package output 把采集结果写成 JSONL 或 CSV。
package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gospider/internal/extract"
)

// Writer 按行写出记录。path 为 "-" 时写到标准输出。
type Writer struct {
	format string
	cols   []string
	fixed  bool
	w      io.Writer
	file   *os.File
	cw     *csv.Writer
	mu     sync.Mutex
	n      int
	header bool
}

// New 创建输出。已存在的文件会被覆盖。
func New(format, path string, cols []string) (*Writer, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "jsonl"
	}
	if format == "stdout" {
		format = "jsonl"
		path = "-"
	}
	if format != "jsonl" && format != "csv" {
		return nil, fmt.Errorf("不支持的输出格式 %s（可用 jsonl、csv、stdout）", format)
	}
	w := &Writer{format: format, cols: append([]string(nil), cols...), fixed: len(cols) > 0}
	if path == "" || path == "-" {
		w.w = os.Stdout
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("创建输出目录: %w", err)
		}
		f, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		w.file = f
		w.w = f
	}
	if format == "csv" {
		w.cw = csv.NewWriter(w.w)
	}
	return w, nil
}

// NewBuffer 供测试把结果放进内存。
func NewBuffer(format string, buf *bytes.Buffer, cols []string) *Writer {
	if format == "" {
		format = "jsonl"
	}
	w := &Writer{format: format, cols: append([]string(nil), cols...), fixed: len(cols) > 0, w: buf}
	if format == "csv" {
		w.cw = csv.NewWriter(buf)
	}
	return w
}

// Write 写一条记录。
func (w *Writer) Write(item map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var err error
	switch w.format {
	case "csv":
		err = w.writeCSV(item)
	default:
		err = w.writeJSON(item)
	}
	if err != nil {
		return err
	}
	w.n++
	if w.file != nil && w.n%20 == 0 {
		_ = w.file.Sync()
	}
	return nil
}

func (w *Writer) writeJSON(item map[string]any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(item); err != nil {
		return err
	}
	_, err := w.w.Write(buf.Bytes())
	return err
}

func (w *Writer) writeCSV(item map[string]any) error {
	if len(w.cols) == 0 {
		w.cols = sortedKeys(item)
	} else if !w.fixed {
		seen := map[string]struct{}{}
		for _, c := range w.cols {
			seen[c] = struct{}{}
		}
		var extra []string
		for k := range item {
			if _, ok := seen[k]; !ok {
				extra = append(extra, k)
			}
		}
		sort.Strings(extra)
		w.cols = append(w.cols, extra...)
	}
	if !w.header {
		if err := w.cw.Write(w.cols); err != nil {
			return err
		}
		w.header = true
	}
	row := make([]string, len(w.cols))
	for i, c := range w.cols {
		row[i] = extract.Stringify(item[c])
	}
	if err := w.cw.Write(row); err != nil {
		return err
	}
	w.cw.Flush()
	return w.cw.Error()
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Close 刷出缓冲并关闭文件。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cw != nil {
		w.cw.Flush()
		if err := w.cw.Error(); err != nil {
			return err
		}
	}
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}
