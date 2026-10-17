package job

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestAlive(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatal("current process should be alive")
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
	if Alive(0) {
		t.Fatal("pid 0")
	}
}

func TestProgressAndDuration(t *testing.T) {
	s := Snapshot{Total: 4, Done: 2, Queued: 1}
	if Progress(s) != "2/4 50%" {
		t.Fatal(Progress(s))
	}
	s.Total = 0
	if Progress(s) != "2 请求，队列 1" {
		t.Fatal(Progress(s))
	}
	start := time.Now().Add(-90 * time.Second)
	got := FormatDuration(Elapsed(Snapshot{Status: "running", StartedAt: start}, time.Now()))
	if got != "1:30" && got != "1:29" && got != "1:31" {
		t.Fatal(got)
	}
}
