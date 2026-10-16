package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gospider/internal/job"
)

func TestRunAndPS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			if n, err := strconv.Atoi(p); err == nil {
				page = n
			}
		}
		all := []map[string]any{{"id": 1, "name": "a"}, {"id": 2, "name": "b"}, {"id": 3, "name": "c"}}
		size := 2
		start := (page - 1) * size
		if start < 0 {
			start = 0
		}
		end := start + size
		if start > len(all) {
			start = len(all)
		}
		if end > len(all) {
			end = len(all)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total": len(all), "list": all[start:end]})
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("GOSPIDER_HOME", filepath.Join(dir, "home"))
	out := filepath.Join(dir, "out.jsonl")
	cfgPath := filepath.Join(dir, "task.yaml")
	body := strings.ReplaceAll(`
name: cli-demo
mode: api
workers: 2
timeout: 5s
retry: 0
request:
  url: SERVER/list
  method: GET
page:
  param: page
  size: 2
  total: total
list: list
fields:
  - name: id
  - name: name
output:
  format: jsonl
  path: OUT
`, "SERVER", srv.URL)
	body = strings.ReplaceAll(body, "OUT", out)
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	oldOut, oldErr := Stdout, Stderr
	Stdout, Stderr = &stdout, &stderr
	t.Cleanup(func() { Stdout, Stderr = oldOut, oldErr })

	if code := Run([]string{"run", "-c", cfgPath, "-q"}); code != 0 {
		t.Fatalf("run code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "完成") || !strings.Contains(stderr.String(), "条目 3") {
		t.Fatalf("stderr=%s", stderr.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "\n") != 3 {
		t.Fatalf("output=%s", b)
	}

	stdout.Reset()
	if code := Run([]string{"ps"}); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(stdout.String(), "没有正在采集") {
		t.Fatalf("ps=%s", stdout.String())
	}
	stdout.Reset()
	if code := Run([]string{"ps", "-a"}); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(stdout.String(), "cli-demo") || !strings.Contains(stdout.String(), "完成") {
		t.Fatalf("ps -a=%s", stdout.String())
	}
}

func TestStopSleep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOSPIDER_HOME", home)
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
	})
	id := "stopme01"
	if err := job.Save(job.Snapshot{
		ID:        id,
		Name:      "sleep",
		PID:       cmd.Process.Pid,
		Status:    "running",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := Stdout, Stderr
	Stdout, Stderr = &stdout, &stderr
	t.Cleanup(func() { Stdout, Stderr = oldOut, oldErr })

	if code := Run([]string{"stop", "stop"}); code != 0 {
		t.Fatalf("stop code=%d %s", code, stderr.String())
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("process still running")
	}
	if !strings.Contains(stdout.String(), "已通知") {
		t.Fatalf("stdout=%s", stdout.String())
	}
}
