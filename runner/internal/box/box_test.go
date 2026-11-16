package box

import (
	"errors"
	"reflect"
	"syscall"
	"testing"
	"time"

	"serverless-judge/runner/proto"
)

func TestSplitWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  -O2   -Wall ", []string{"-O2", "-Wall"}},
		{`-DNAME="a b" 'c d' e\ f`, []string{"-DNAME=a b", "c d", "e f"}},
		{`""`, []string{""}},
		{`a "b\"c"`, []string{"a", `b"c`}},
	}
	for _, c := range cases {
		if got := SplitWords(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitWords(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpand(t *testing.T) {
	got := Expand(
		[]string{"java", "-Xmx{memory_mb}m", "{options}", "Main", "{args}", "t{test}", "{cpu_s}s"},
		Vars{Options: "-ea -Dx=1", Args: "", MemoryKB: 262144, CPUMs: 1500, Test: 3},
	)
	want := []string{"java", "-Xmx256m", "-ea", "-Dx=1", "Main", "t3", "2s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Expand = %q, want %q", got, want)
	}
}

func TestVerdict(t *testing.T) {
	const tl, ml = 2000, 262144
	cases := []struct {
		name                        string
		err                         error
		killed                      string
		exit, signal, timeMs, memKB int
		truncated                   bool
		want                        string
	}{
		{name: "clean exit", timeMs: 10, memKB: 3000, want: proto.StatusOK},
		{name: "internal error wins", err: errors.New("x"), want: proto.StatusIE},
		{name: "killed on wall clock", killed: "wall", signal: 9, exit: -1, want: proto.StatusTLE},
		{name: "killed on cpu", killed: "cpu", signal: 9, exit: -1, timeMs: 2044, want: proto.StatusTLE},
		{name: "finished but over the limit", timeMs: 2001, want: proto.StatusTLE},
		{name: "exactly at the limit passes", timeMs: 2000, want: proto.StatusOK},
		{name: "rlimit cpu soft signal", signal: int(syscall.SIGXCPU), exit: -1, timeMs: 1990, want: proto.StatusTLE},
		{name: "killed on memory", killed: "mem", signal: 9, exit: -1, memKB: 270000, want: proto.StatusMLE},
		{name: "peak memory over limit", memKB: ml + 1, want: proto.StatusMLE},
		{name: "time beats memory", timeMs: 3000, memKB: ml + 1, want: proto.StatusTLE},
		{name: "output cut off", truncated: true, want: proto.StatusOLE},
		{name: "filled the filesystem", killed: "disk", signal: 9, exit: -1, want: proto.StatusOLE},
		{name: "file size signal", signal: int(syscall.SIGXFSZ), exit: -1, want: proto.StatusOLE},
		{name: "downloaded more than allowed", killed: "net", signal: 9, exit: -1, want: proto.StatusOLE},
		{name: "wrote more output than allowed on a network run", killed: "out", signal: 9, exit: -1, want: proto.StatusOLE},
		{name: "segfault", signal: int(syscall.SIGSEGV), exit: -1, want: proto.StatusRE},
		{name: "non-zero exit", exit: 3, want: proto.StatusRE},
	}
	for _, c := range cases {
		got, _ := Verdict(c.err, c.killed, c.exit, c.signal, c.timeMs, c.memKB, c.truncated, tl, ml)
		if got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	// With the memory limit switched off, a large peak is not a verdict.
	if got, _ := Verdict(nil, "", 0, 0, 10, 9999999, false, tl, 0); got != proto.StatusOK {
		t.Errorf("no memory limit: got %s, want ok", got)
	}
}

func TestParseReport(t *testing.T) {
	r, ok := ParseReport("1 3 1500 700 2048\n")
	if !ok || !r.exited || r.code != 3 || r.cpuMs != 2 || r.maxRSSKB != 2048 {
		t.Errorf("exit report parsed as %+v ok=%v", r, ok)
	}
	r, ok = ParseReport("0 9 2000000 50000 265040\n")
	if !ok || r.exited || r.code != 9 || r.cpuMs != 2050 || r.maxRSSKB != 265040 {
		t.Errorf("signal report parsed as %+v ok=%v", r, ok)
	}
	// Anything the launcher would not write must be rejected, so a missing
	// or forged report can never become a result.
	for _, bad := range []string{"", "1 0 0 0", "1 0 0 0 0 0", "1 x 0 0 0", "1 -1 0 0 0"} {
		if _, ok := ParseReport(bad); ok {
			t.Errorf("ParseReport(%q) accepted", bad)
		}
	}
}

// fakeSmaps replaces the /proc reader for one test.
func fakeSmaps(t *testing.T, f func(pid int) (int, int, smapsState)) {
	t.Helper()
	old := smapsOf
	smapsOf = f
	t.Cleanup(func() { smapsOf = old })
}

// A JVM that has only loaded itself has about 220 MB resident and 46 MB
// anonymous. Only the anonymous part is the program's memory.
func TestMemWatchCountsAnonymousMemoryOnly(t *testing.T) {
	fakeSmaps(t, func(int) (int, int, smapsState) { return 46_000, 220_000, smapsOK })
	var w memWatch
	got, ok := w.read([]procInfo{{pid: 7, rssKB: 220_000}}, 220_000, 262144, time.Unix(1, 0))
	if !ok || got != 46_000 || w.peakKB != 46_000 || w.fileKB != 174_000 {
		t.Errorf("got anon=%d ok=%v peak=%d file=%d, want 46000 true 46000 174000", got, ok, w.peakKB, w.fileKB)
	}
}

// When the program exits between the cheap sample and the exact one, its map
// is gone. That must not be read as "all 220 MB is program memory": it would
// report nonsense and could fail a correct program near its limit.
func TestMemWatchIgnoresExitingProcess(t *testing.T) {
	alive := true
	fakeSmaps(t, func(int) (int, int, smapsState) {
		if alive {
			return 46_000, 220_000, smapsOK
		}
		return 0, 0, smapsGone
	})
	procs := []procInfo{{pid: 7, rssKB: 300_000}}
	var w memWatch
	w.read(procs, 220_000, 262144, time.Unix(1, 0))
	alive = false
	got, ok := w.read(procs, 300_000, 262144, time.Unix(2, 0))
	if !ok || got != 0 {
		t.Errorf("exiting process counted as %d KB (ok=%v), want 0", got, ok)
	}
	if w.peakKB != 46_000 {
		t.Errorf("peak rose to %d KB on a process that was already exiting, want 46000", w.peakKB)
	}
}

// A program that hides its own map does not get to hide its memory.
func TestMemWatchCountsEverythingWhenMapIsHidden(t *testing.T) {
	fakeSmaps(t, func(int) (int, int, smapsState) { return 0, 0, smapsDenied })
	var w memWatch
	if got, _ := w.read([]procInfo{{pid: 7, rssKB: 300_000}}, 300_000, 262144, time.Unix(1, 0)); got != 300_000 {
		t.Errorf("hidden map counted as %d KB, want all 300000", got)
	}
}

// The schedule of exact readings is what guards against false memory
// verdicts. Here every byte is anonymous, so readings equal resident size.
func TestMemWatchSchedule(t *testing.T) {
	fakeSmaps(t, func(pid int) (int, int, smapsState) { return 0, 0, smapsDenied })
	t0 := time.Unix(1000, 0)
	procs := func(rss int) []procInfo { return []procInfo{{pid: 1, rssKB: rss}} }
	var w memWatch

	if _, ok := w.read(procs(10_000), 10_000, 262144, t0); !ok {
		t.Fatal("the first tick must take an exact reading")
	}
	if _, ok := w.read(procs(10_500), 10_500, 262144, t0.Add(4*time.Millisecond)); ok {
		t.Error("no reading is due 4 ms later with little growth")
	}
	if _, ok := w.read(procs(20_000), 20_000, 262144, t0.Add(4*time.Millisecond)); ok {
		t.Error("growth alone must not trigger a reading inside the minimum gap")
	}
	if _, ok := w.read(procs(20_000), 20_000, 262144, t0.Add(9*time.Millisecond)); !ok {
		t.Error("4 MB of growth after the minimum gap must trigger a reading")
	}
	if _, ok := w.read(procs(20_100), 20_100, 262144, t0.Add(120*time.Millisecond)); !ok {
		t.Error("a reading is due after the periodic interval")
	}
	// The estimate (resident minus last known file-backed) crossing the
	// limit must force an exact reading at once, even inside the gap.
	w = memWatch{fileKB: 100_000, lastAt: t0, lastRSS: 300_000, sampled: true}
	if _, ok := w.read(procs(300_000), 300_000, 262144, t0.Add(time.Millisecond)); ok {
		t.Error("estimate of 200 MB is under a 256 MB limit: no reading due")
	}
	if _, ok := w.read(procs(363_000), 363_000, 262144, t0.Add(2*time.Millisecond)); !ok {
		t.Error("estimate over the limit must force an exact reading")
	}
	if w.peakKB != 363_000 {
		t.Errorf("peak should follow exact readings, got %d", w.peakKB)
	}
}

func TestNetCreditMs(t *testing.T) {
	cases := []struct{ rxKB, usPerMB, want int }{
		{0, 15000, 0},
		{1024, 15000, 15},               // 1 MB at 15 ms per MB
		{125 * 1024, 15000, 1875},       // a full 125 MB download
		{512, 15000, 7},                 // half a MB, rounded down
		{125 * 1024, 0, 0},              // credit switched off
		{-5, 15000, 0},                  // a counter that went backwards
		{1 << 20, 1000 * 1000, 1024000}, // the largest values the runner accepts
	}
	for _, c := range cases {
		if got := netCreditMs(c.rxKB, c.usPerMB); got != c.want {
			t.Errorf("netCreditMs(%d, %d) = %d, want %d", c.rxKB, c.usPerMB, got, c.want)
		}
	}
}
