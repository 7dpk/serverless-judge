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

// fakeLauncher stands in for the Cloud Run sandbox command: it logs how it
// was called, applies the -e variables, and runs what follows "--".
func fakeLauncher(t *testing.T) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "sandbox"), filepath.Join(dir, "launches.log")
	script := `#!/bin/sh
echo "$@" >> "` + log + `"
[ "$1" = do ] || exit 0
shift
while [ $# -gt 0 ]; do
  case "$1" in
    --) shift; break ;;
    -e) export "$2"; shift 2 ;;
    --sandbox-name|-w) shift 2 ;;
    *) shift ;;
  esac
done
exec "$@"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func TestSandboxArgs(t *testing.T) {
	plain := strings.Join(sandboxArgs("j1", false, false), " ")
	if plain != "do --sandbox-name j1 -w /" {
		t.Errorf("plain sandbox: %q", plain)
	}
	if strings.Contains(strings.Join(sandboxArgs("j1", true, false), " "), "egress") {
		t.Error("a sandbox without network was given egress")
	}
	if got := strings.Join(sandboxArgs("j1", true, true), " "); got != "do --sandbox-name j1 -w / --write --allow-egress" {
		t.Errorf("network sandbox: %q", got)
	}
}

func TestPoolNetworkJobGetsItsOwnEgressSandbox(t *testing.T) {
	box, boxLog := fakeBox(t, "")
	launcher, launchLog := fakeLauncher(t)
	p := NewPool(launcher, box, "/langs", false, map[string]string{
		"PATH": os.Getenv("PATH"), "FAKE_LOG": boxLog, "FAKE_MODE": "",
	})
	defer p.Close()

	p.Refill()
	p.mu.Lock()
	spare := p.spare
	p.mu.Unlock()
	if spare == nil {
		t.Fatal("no spare sandbox after Refill")
	}

	netJob := job()
	netJob.Req.Network = &proto.Network{WallMs: 1000, RxKB: 1024, FileKB: 1024}
	resp, err := p.Run(netJob, 5*time.Second)
	if err != nil {
		t.Fatalf("network job: %v", err)
	}
	if !resp.Meta.Network || resp.Meta.Warm {
		t.Errorf("network job: meta says network=%v warm=%v, want true and false", resp.Meta.Network, resp.Meta.Warm)
	}
	// The spare has no network and must not have been used for this job.
	p.mu.Lock()
	still := p.spare
	p.mu.Unlock()
	if still != spare {
		t.Error("the network job took the spare sandbox")
	}

	if resp, err = p.Run(job(), 5*time.Second); err != nil {
		t.Fatalf("ordinary job: %v", err)
	}
	if resp.Meta.Network {
		t.Error("an ordinary job reported network access")
	}

	data, _ := os.ReadFile(launchLog)
	egress := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "do ") && strings.Contains(line, "--allow-egress") {
			egress++
		}
	}
	if egress != 1 {
		t.Errorf("want exactly one sandbox with egress, got %d:\n%s", egress, data)
	}
}
