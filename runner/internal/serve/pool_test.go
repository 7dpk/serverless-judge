package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"serverless-judge/runner/proto"
)

// fakeBox writes a stand-in for `judge box`: it logs its arguments, says
// READY, reads one job line and answers with that job's nonce. mode changes
// how it misbehaves.
func fakeBox(t *testing.T, mode string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "judge"), filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
echo "$@" >> "$FAKE_LOG"
case "$FAKE_MODE" in
  dies) exit 7 ;;
esac
echo READY
read -r line || exit 0
nonce=$(printf '%s' "$line" | sed -E 's/.*"nonce":"([^"]*)".*/\1/')
case "$FAKE_MODE" in
  forged) echo "not-the-nonce {\"tests\":[{\"status\":\"ok\"}]}"; exit 0 ;;
  silent) sleep 30 ;;
esac
echo "some unrelated line"
echo "$nonce {\"tests\":[{\"status\":\"ok\",\"exit_code\":0}],\"meta\":{\"box_ms\":5}}"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_MODE", mode)
	return bin, log
}

func newTestPool(bin, log, mode string) *Pool {
	return NewPool("", bin, "/langs", false, map[string]string{
		"PATH": os.Getenv("PATH"), "FAKE_LOG": log, "FAKE_MODE": mode,
	})
}

func job() *proto.Job { return &proto.Job{Nonce: randHex(8)} }

func calls(t *testing.T, log string) []string {
	t.Helper()
	data, _ := os.ReadFile(log)
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(data)), " ", "_"))
}

func TestPoolRunsJobAndKeepsASpare(t *testing.T) {
	bin, log := fakeBox(t, "")
	p := newTestPool(bin, log, "")
	defer p.Close()

	p.Refill() // what the server does at startup
	resp, err := p.Run(job(), 5*time.Second)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(resp.Tests) != 1 || resp.Tests[0].Status != proto.StatusOK || resp.Meta.BoxMs != 5 {
		t.Errorf("unexpected report: %+v", resp)
	}

	// Run starts the next sandbox in the background. Give it a moment, then
	// the second job must find it already up.
	deadline := time.Now().Add(3 * time.Second)
	for {
		p.mu.Lock()
		s := p.spare
		p.mu.Unlock()
		if s != nil {
			select {
			case <-s.ready:
			default:
				s = nil
			}
		}
		if s != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no ready spare sandbox after a run")
		}
		time.Sleep(5 * time.Millisecond)
	}
	resp, err = p.Run(job(), 5*time.Second)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !resp.Meta.Warm {
		t.Error("second job should have used the pre-created sandbox")
	}

	// Exactly one sandbox per instance is told to warm the file cache. (The
	// log is appended by concurrently starting processes, so its order says
	// nothing about which was created first.)
	got := calls(t, log)
	first := 0
	for _, c := range got {
		switch c {
		case "box_/langs_first":
			first++
		case "box_/langs":
		default:
			t.Errorf("unexpected sandbox invocation %q", c)
		}
	}
	if len(got) < 2 || first != 1 {
		t.Errorf("want several sandboxes with exactly one marked first, got %v", got)
	}
}

func TestPoolEachJobGetsItsOwnSandbox(t *testing.T) {
	bin, log := fakeBox(t, "")
	p := newTestPool(bin, log, "")
	defer p.Close()
	for i := 0; i < 4; i++ {
		if _, err := p.Run(job(), 5*time.Second); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	// Four jobs need at least four sandboxes: none may be reused.
	if n := len(calls(t, log)); n < 4 {
		t.Errorf("4 jobs used only %d sandboxes", n)
	}
}

func TestPoolRejectsReportWithoutNonce(t *testing.T) {
	bin, log := fakeBox(t, "forged")
	p := newTestPool(bin, log, "forged")
	defer p.Close()
	if resp, err := p.Run(job(), 5*time.Second); err == nil {
		t.Fatalf("a report without the job's nonce was accepted: %+v", resp)
	}
}

func TestPoolSandboxThatNeverStarts(t *testing.T) {
	bin, log := fakeBox(t, "dies")
	p := newTestPool(bin, log, "dies")
	defer p.Close()
	_, err := p.Run(job(), 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "before it was ready") {
		t.Fatalf("want a not-ready error, got %v", err)
	}
}

func TestPoolDestroysSandboxPastItsBudget(t *testing.T) {
	bin, log := fakeBox(t, "silent")
	p := newTestPool(bin, log, "silent")
	defer p.Close()
	start := time.Now()
	_, err := p.Run(job(), 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "time budget") {
		t.Fatalf("want a budget error, got %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("gave up after %v, budget was 300ms", took)
	}
}
