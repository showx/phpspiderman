package job

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveListAndDeadPID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOSPIDER_HOME", home)

	s := Snapshot{
		ID:        "abc123",
		Name:      "demo",
		PID:       os.Getpid(),
		Status:    "running",
		StartedAt: time.Now().Add(-2 * time.Second),
		Output:    "data/out.jsonl",
	}
	if err := Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := Load("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "demo" || !Alive(got.PID) {
		t.Fatalf("load = %+v alive=%v", got, Alive(got.PID))
	}
	if _, err := os.Stat(filepath.Join(home, "jobs", "abc123.json")); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sleep", "0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	dead := cmd.Process.Pid
	_ = cmd.Wait()
	if Alive(dead) {
		t.Fatal("exited process still looks alive")
	}
	if err := AppendLog("abc123", "失败一\n行"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(LogPath("abc123"))
	if err != nil || len(b) == 0 {
		t.Fatalf("log %q %v", b, err)
	}
	list, err := List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v %v", list, err)
	}
	found, err := Resolve("abc")
	if err != nil || found.ID != "abc123" {
		t.Fatalf("resolve %v %v", found, err)
	}
}
