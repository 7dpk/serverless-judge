// Package box is the supervisor that runs inside a sandbox, next to the
// untrusted program. It compiles, runs each test under limits, measures time
// and memory, and writes one report line to stdout.
//
// Trust model: the sandbox is the security boundary. The supervisor shares it
// with the code it measures, so it protects its own report rather than the
// host: it is non-dumpable (the program cannot read its memory or open its
// stdout), it never hands its stdout to a child, and the report line is
// prefixed with a nonce that only arrives on stdin. If the program kills the
// supervisor there is no report and the run fails closed.
package box

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"serverless-judge/runner/internal/netstat"
	"serverless-judge/runner/proto"
)

const (
	workDir = "/tmp/box"
	ctlDir  = "/tmp/.judge"
	homeDir = "/tmp/home"

	sampleEvery = 4 * time.Millisecond
	cpuGraceMs  = 40

	// Anonymous-memory sampling (see memWatch).
	detailEvery    = 100 * time.Millisecond
	detailMinGap   = 8 * time.Millisecond
	detailGrowthKB = 4 * 1024
	// Total resident memory may exceed the limit by this much before the
	// run is stopped regardless, to protect the instance.
	residentSlackKB = 768 * 1024

	// Files in a sandbox live in the instance's memory. A run may add this
	// much across all of its tests before it is stopped.
	runDiskKB     = 256 * 1024
	diskEveryTick = 8
	// A run with network access: how often to read the receive counter and
	// the size of the output files, in ticks.
	netEveryTick = 4
	// The file limit of such a run is what it may download, so the disk has
	// to hold that much and a little more.
	netDiskSlackKB = 64 * 1024

	compileCPUMs  = 20000
	compileWallMs = 30000
	compileMemKB  = 1536 * 1024
	hookWallMs    = 30000 // prepare, before_run, after_run
	phaseOutKB    = 128
	bigFileKB     = 1 << 20 // build artifacts, database files
	defaultProcs  = 256
)

// jexecBin sits next to the judge binary in every image.
var jexecBin = func() string {
	if self, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(self), "jexec")
	}
	return "/opt/judge/jexec"
}()

type boxer struct {
	job      *proto.Job
	m        *proto.Manifest
	baseline map[int]bool
	self     int
	helper   int // pid of the jexec launcher for the step in flight
	diskBase int // filesystem usage when the first test started, -1 before
	seq      int
	baseEnv  []string
}

type step struct {
	argv     []string
	stdin    []byte
	hasStdin bool
	cpuMs    int // 0: no CPU limit
	wallMs   int
	memKB    int // 0: no memory limit
	asKB     int
	outKB    int
	fsizeKB  int
	stackKB  int
	procs    int
	merge    bool
	keep     bool // leave surviving processes running (daemons)
	anon     bool // measure anonymous memory rather than everything resident
	diskKB   int  // cap on filesystem growth since the first test; 0: none
	env      []string
	// strictOut marks a test run: more output than outKB is a verdict, not
	// something to cut off quietly.
	strictOut bool
	// Set only for a run with network access (see proto.Network).
	netRxKB    int // most it may receive; 0: no network
	netUsPerMB int // CPU credited per MB received
}

// How a read of /proc/<pid>/smaps ended.
type smapsState int

const (
	smapsOK     smapsState = iota
	smapsGone              // no such process any more, or nothing left to list
	smapsDenied            // the process exists but will not show its map
)

// smapsOf is readSmaps, replaceable in tests.
var smapsOf = readSmaps

// memWatch tracks a run's memory the way a judge should report it: the
// program's own anonymous memory (heap and stacks), not the runtime's code.
//
// Two properties of the sandbox make plain resident size misleading. It
// accounts memory in 2 MB chunks, and it counts file-backed pages, so a JVM
// that has merely loaded itself reads as about 220 MB and Node as about
// 125 MB. Their anonymous memory is about 45 MB and 25 MB.
//
// Anonymous size is only available from /proc/<pid>/smaps, which is too
// costly to read every few milliseconds. So smaps is read when it matters:
// at the start, after resident size grows, periodically, and always before
// a memory verdict. Only those exact readings are reported or acted on.
type memWatch struct {
	fileKB  int // file-backed resident memory at the last exact reading
	lastAt  time.Time
	lastRSS int
	peakKB  int
	sampled bool
}

// read returns the exact anonymous memory of procs if a reading is due.
func (w *memWatch) read(procs []procInfo, rssKB, limitKB int, now time.Time) (int, bool) {
	due := !w.sampled ||
		now.Sub(w.lastAt) >= detailEvery ||
		(rssKB-w.lastRSS >= detailGrowthKB && now.Sub(w.lastAt) >= detailMinGap) ||
		(limitKB > 0 && rssKB-w.fileKB > limitKB)
	if !due {
		return 0, false
	}
	anon, resident := 0, 0
	for _, p := range procs {
		a, r, state := smapsOf(p.pid)
		switch state {
		case smapsGone:
			// Exiting: its memory is already being torn down. Counting its
			// last resident size here would report a JVM's 220 MB of mapped
			// runtime as program memory, and could fail a correct program.
			continue
		case smapsDenied:
			// It made its own map unreadable (non-dumpable). That is the
			// program's choice, so all of its resident memory counts.
			a, r = p.rssKB, p.rssKB
		}
		anon += a
		resident += r
	}
	w.fileKB, w.lastAt, w.lastRSS, w.sampled = resident-anon, now, rssKB, true
	if anon > w.peakKB {
		w.peakKB = anon
	}
	return anon, true
}

type outcome struct {
	exit, signal         int
	killed               string
	cpuMs, wallMs, memKB int
	stdout, stderr       []byte
	truncated            bool
	err                  error
	netRxKB, creditMs    int // network runs: received, and CPU credited for it
}

// netCreditMs is the CPU time a run is not charged for having received rxKB
// over the network.
func netCreditMs(rxKB, usPerMB int) int {
	if rxKB <= 0 || usPerMB <= 0 {
		return 0
	}
	return int(int64(rxKB) * int64(usPerMB) / 1024 / 1000)
}

// Main is the entry point for `judge box`.
func Main() {
	setNonDumpable()
	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	if len(os.Args) > 2 {
		warmup(os.Args[2], len(os.Args) > 3 && os.Args[3] == "first")
	}
	fmt.Fprintln(out, "READY")
	out.Flush()

	var job proto.Job
	if err := json.NewDecoder(bufio.NewReaderSize(os.Stdin, 1<<20)).Decode(&job); err != nil {
		fmt.Fprintln(os.Stderr, "judge box: no job:", err)
		os.Exit(2)
	}
	t0 := time.Now()
	resp := run(&job)
	resp.Meta.BoxMs = int(time.Since(t0) / time.Millisecond)

	b, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "judge box: encode:", err)
		os.Exit(3)
	}
	out.WriteString(job.Nonce)
	out.WriteByte(' ')
	out.Write(b)
	out.WriteByte('\n')
	out.Flush()
}

// warmup runs manifest warmup commands before the sandbox reports READY.
//
// There are two kinds. A daemon warmup starts something the job needs
// running, a database engine for example, and runs in every sandbox. An
// ordinary warmup only pulls the toolchain into the instance's file cache,
// which all sandboxes share, so it runs once per instance (first == true).
// Running it in every sandbox was measured to buy nothing and to steal CPU
// from the job in flight.
func warmup(langDir string, first bool) {
	paths, _ := filepath.Glob(filepath.Join(langDir, "*.json"))
	sort.Strings(paths)
	seen := map[string]bool{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m proto.Manifest
		if json.Unmarshal(data, &m) != nil || m.Warmup == nil || len(m.Warmup.Cmd) == 0 {
			continue
		}
		if !m.Warmup.Daemon && !first {
			continue
		}
		key := strings.Join(m.Warmup.Cmd, "\x00")
		if seen[key] {
			continue // languages sharing an image often share a warmup
		}
		seen[key] = true
		for _, d := range []string{workDir, ctlDir, homeDir} {
			os.MkdirAll(d, 0o755)
		}
		job := &proto.Job{Manifest: m}
		b := &boxer{job: job, m: &m, self: os.Getpid(), baseline: pidSet()}
		b.baseEnv = b.buildEnv()
		st := b.hook(m.Warmup, Vars{}, 0, 30000, 0)
		st.keep = m.Warmup.Daemon
		b.exec(st)
	}
	os.RemoveAll(workDir)
}

func run(job *proto.Job) *proto.RunResponse {
	resp := &proto.RunResponse{}
	m := &job.Manifest
	for _, d := range []string{workDir, ctlDir, homeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			resp.Error = "mkdir: " + err.Error()
			return resp
		}
	}
	if err := writeFiles(m, &job.Req); err != nil {
		resp.Error = "write source: " + err.Error()
		return resp
	}

	b := &boxer{job: job, m: m, self: os.Getpid(), baseline: pidSet(), diskBase: -1}
	b.baseEnv = b.buildEnv()
	lim := job.Req.Limits
	vars := Vars{Options: job.Req.CompilerOptions, Args: job.Req.Args, MemoryKB: lim.MemoryKB, CPUMs: lim.CPUMs}

	if m.Compile != nil {
		s := b.hook(m.Compile, vars, compileCPUMs, compileWallMs, compileMemKB)
		ph := phase(b.exec(s))
		resp.Compile = ph
		if !ph.OK {
			return resp
		}
	}
	if m.Prepare != nil {
		s := b.hook(m.Prepare, vars, 0, hookWallMs, 0)
		s.keep = true
		ph := phase(b.exec(s))
		if !ph.OK {
			resp.Error = "prepare step failed: " + tail(ph.Output, 2000)
			return resp
		}
		b.baseline = pidSet() // whatever prepare left running is a daemon
	}
	for i, t := range job.Req.Tests {
		resp.Tests = append(resp.Tests, b.test(i, t, vars))
	}
	return resp
}

func (b *boxer) test(i int, t proto.Test, vars Vars) proto.TestResult {
	m, lim := b.m, b.job.Req.Limits
	vars.Test = i
	testEnv := append(append([]string{}, b.baseEnv...), "JUDGE_TEST="+strconv.Itoa(i))

	if m.AfterRun != nil {
		defer func() {
			s := b.hook(m.AfterRun, vars, 0, hookWallMs, 0)
			s.env = testEnv
			b.exec(s)
		}()
	}

	stdin, hasStdin := t.Stdin, true
	if m.BeforeRun != nil {
		s := b.hook(m.BeforeRun, vars, 0, hookWallMs, 0)
		s.env = testEnv
		s.stdin, s.hasStdin = t.Stdin, true
		o := b.exec(s)
		if o.err != nil || o.killed != "" || o.signal != 0 || o.exit != 0 {
			return proto.TestResult{
				Status: proto.StatusIE, ExitCode: o.exit, Signal: o.signal,
				Stderr: o.stdout, WallMs: o.wallMs,
				Message: "setup step failed" + reason(o),
			}
		}
		stdin, hasStdin = nil, false
	}

	s := step{
		argv:      Expand(m.Run.Cmd, vars),
		stdin:     stdin,
		hasStdin:  hasStdin,
		cpuMs:     lim.CPUMs,
		wallMs:    lim.WallMs,
		memKB:     lim.MemoryKB,
		outKB:     lim.OutputKB,
		fsizeKB:   lim.OutputKB,
		stackKB:   m.StackKB,
		procs:     lim.Procs,
		merge:     b.job.Req.MergeStderr,
		anon:      true,
		diskKB:    runDiskKB,
		env:       testEnv,
		strictOut: true,
	}
	if m.WallClock {
		s.cpuMs, s.wallMs = 0, lim.CPUMs
	} else {
		s.cpuMs += m.RuntimeCPUMs
	}
	// With network access the program may wait on the network for longer
	// than it may compute, and may write a file as large as it may download.
	net := b.job.Req.Network
	if net != nil {
		s.wallMs += net.WallMs
		s.netRxKB, s.netUsPerMB = net.RxKB, net.CPUUsPerMB
		if net.FileKB > s.fsizeKB {
			s.fsizeKB = net.FileKB
		}
		if s.fsizeKB+netDiskSlackKB > s.diskKB {
			s.diskKB = s.fsizeKB + netDiskSlackKB
		}
	}
	if m.NoMemoryLimit {
		s.memKB = 0
	}
	if m.AddressSpace && lim.MemoryKB > 0 {
		s.asKB = lim.MemoryKB + m.AddressSpaceExtraKB
	}
	o := b.exec(s)

	res := proto.TestResult{
		ExitCode: o.exit, Signal: o.signal,
		Stdout: o.stdout, Stderr: o.stderr,
		CPUMs: o.cpuMs, WallMs: o.wallMs, MemoryKB: o.memKB,
		NetRxKB: o.netRxKB,
	}
	if m.WallClock {
		res.CPUMs = o.wallMs
	} else {
		// Neither the runtime's own start nor the cost of receiving data
		// is the program's work.
		res.CPUMs -= min(m.RuntimeCPUMs, res.CPUMs)
		res.NetCreditMs = min(o.creditMs, res.CPUMs)
		res.CPUMs -= res.NetCreditMs
	}
	res.Status, res.Message = Verdict(o.err, o.killed, o.exit, o.signal, res.CPUMs, o.memKB, o.truncated, lim.CPUMs, s.memKB)
	if net != nil && res.Status == proto.StatusOLE && o.signal == int(syscall.SIGXFSZ) {
		res.Message = "file size limit exceeded"
	}
	return res
}

// Verdict maps what happened to a test status. timeMs is CPU time, or wall
// time for wall-clock languages.
func Verdict(err error, killed string, exit, signal, timeMs, memKB int, truncated bool, timeLimitMs, memLimitKB int) (string, string) {
	switch {
	case err != nil:
		return proto.StatusIE, err.Error()
	case killed == "wall":
		return proto.StatusTLE, "wall time limit exceeded"
	case killed == "cpu" || timeMs > timeLimitMs || signal == int(syscall.SIGXCPU):
		return proto.StatusTLE, "time limit exceeded"
	case killed == "mem" || (memLimitKB > 0 && memKB > memLimitKB):
		return proto.StatusMLE, "memory limit exceeded"
	case killed == "disk":
		return proto.StatusOLE, "disk limit exceeded"
	case killed == "net":
		return proto.StatusOLE, "download limit exceeded"
	case killed == "out":
		return proto.StatusOLE, "output limit exceeded"
	case signal == int(syscall.SIGXFSZ) || truncated:
		return proto.StatusOLE, "output limit exceeded"
	case signal != 0:
		return proto.StatusRE, "killed by signal " + strconv.Itoa(signal)
	case exit != 0:
		return proto.StatusRE, "exited with code " + strconv.Itoa(exit)
	}
	return proto.StatusOK, ""
}

// hook builds a step for a manifest command that is not a timed test run.
func (b *boxer) hook(st *proto.Step, vars Vars, cpuMs, wallMs, memKB int) step {
	if st.CPUMs > 0 {
		cpuMs = st.CPUMs
	}
	if st.WallMs > 0 {
		wallMs = st.WallMs
	}
	if st.MemoryKB > 0 {
		memKB = st.MemoryKB
	}
	return step{
		argv:    Expand(st.Cmd, vars),
		cpuMs:   cpuMs,
		wallMs:  wallMs,
		memKB:   memKB,
		outKB:   phaseOutKB,
		fsizeKB: bigFileKB,
		procs:   1024,
		merge:   true,
		env:     b.baseEnv,
	}
}

func phase(o outcome) *proto.Phase {
	ph := &proto.Phase{
		OK:       o.err == nil && o.killed == "" && o.signal == 0 && o.exit == 0,
		Output:   o.stdout,
		ExitCode: o.exit, Signal: o.signal, Killed: o.killed,
		CPUMs: o.cpuMs, WallMs: o.wallMs, MemoryKB: o.memKB,
	}
	if o.err != nil {
		ph.Output = append(ph.Output, []byte("\njudge: "+o.err.Error())...)
	}
	switch o.killed {
	case "wall", "cpu":
		ph.Output = append(ph.Output, []byte("\njudge: step exceeded its time limit")...)
	case "mem":
		ph.Output = append(ph.Output, []byte("\njudge: step exceeded its memory limit")...)
	}
	return ph
}

func reason(o outcome) string {
	switch {
	case o.err != nil:
		return ": " + o.err.Error()
	case o.killed != "":
		return ": exceeded " + o.killed + " limit"
	case o.signal != 0:
		return ": signal " + strconv.Itoa(o.signal)
	}
	return ": exit code " + strconv.Itoa(o.exit)
}

// exec runs one step under jexec and watches it until it ends.
func (b *boxer) exec(s step) outcome {
	var o outcome
	if len(s.argv) == 0 {
		o.err = fmt.Errorf("empty command")
		return o
	}
	b.seq++
	n := strconv.Itoa(b.seq)
	outPath, errPath := filepath.Join(ctlDir, "out"+n), filepath.Join(ctlDir, "err"+n)
	defer os.Remove(outPath)
	defer os.Remove(errPath)

	args := []string{"-r", "3", "-d", workDir, "-o", outPath}
	if s.hasStdin {
		inPath := filepath.Join(ctlDir, "in"+n)
		if err := os.WriteFile(inPath, s.stdin, 0o600); err != nil {
			o.err = err
			return o
		}
		defer os.Remove(inPath)
		args = append(args, "-i", inPath)
	}
	if s.merge {
		args = append(args, "-m")
	} else {
		args = append(args, "-e", errPath)
	}
	if s.cpuMs > 0 {
		// The kernel's own CPU limit, as a backstop to the sampler below. A
		// network run may earn credit up to what its download cap is worth.
		args = append(args, "-c", strconv.Itoa((s.cpuMs+netCreditMs(s.netRxKB, s.netUsPerMB)+999)/1000+1))
	}
	if s.asKB > 0 {
		args = append(args, "-a", strconv.Itoa(s.asKB))
	}
	if s.fsizeKB > 0 {
		args = append(args, "-f", strconv.Itoa(s.fsizeKB))
	}
	if s.stackKB > 0 {
		args = append(args, "-s", strconv.Itoa(s.stackKB))
	}
	procs := s.procs
	if procs <= 0 {
		procs = defaultProcs
	}
	// RLIMIT_NPROC counts every task of this user in the sandbox, so allow
	// for what is already running.
	args = append(args, "-p", strconv.Itoa(procs+len(b.baseline)+32))
	args = append(args, "--")
	args = append(args, s.argv...)

	repR, repW, err := os.Pipe()
	if err != nil {
		o.err = err
		return o
	}
	defer repR.Close()
	cmd := exec.Command(jexecBin, args...)
	cmd.Env = s.env
	cmd.ExtraFiles = []*os.File{repW} // fd 3 in the launcher
	start := time.Now()
	if err := cmd.Start(); err != nil {
		repW.Close()
		o.err = err
		return o
	}
	repW.Close()
	b.helper = cmd.Process.Pid
	defer func() { b.helper = 0 }()
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()

	tick := time.NewTicker(sampleEvery)
	defer tick.Stop()
	peakKB, liveCPU, ticks := 0, 0, 0
	var watch memWatch
	if s.diskKB > 0 && b.diskBase < 0 {
		b.diskBase = diskUsedKB()
	}
	// Network runs: what has arrived since the step began. The counter is
	// the sandbox's own, so it sees every process in it.
	rxBase, netOK := uint64(0), false
	if s.netRxKB > 0 {
		rxBase, netOK = netstat.RxBytes()
	}
	rxKB := func() int {
		if now, ok := netstat.RxBytes(); ok && now > rxBase {
			return int((now - rxBase) / 1024)
		}
		return 0
	}
	credit := 0
loop:
	for {
		select {
		case <-done:
			break loop
		case now := <-tick.C:
			procs := b.owned()
			rss, cpu := 0, 0
			for _, p := range procs {
				rss += p.rssKB
				cpu += p.cpuMs
			}
			if rss > peakKB {
				peakKB = rss
			}
			if cpu > liveCPU {
				liveCPU = cpu
			}
			// mem is what the limit applies to; known is false between
			// exact readings, when no memory verdict may be given.
			mem, known := rss, true
			if s.anon {
				mem, known = watch.read(procs, rss, s.memKB, now)
			}
			if o.killed == "" {
				wall := int(time.Since(start) / time.Millisecond)
				switch {
				case s.wallMs > 0 && wall > s.wallMs:
					o.killed = "wall"
				case s.cpuMs > 0 && cpu > s.cpuMs+credit+cpuGraceMs:
					o.killed = "cpu"
				case s.memKB > 0 && known && mem > s.memKB:
					o.killed = "mem"
				case s.memKB > 0 && rss > s.memKB+residentSlackKB:
					o.killed = "mem"
				}
				ticks++
				if o.killed == "" && s.diskKB > 0 && ticks%diskEveryTick == 0 && diskUsedKB()-b.diskBase > s.diskKB {
					o.killed = "disk"
				}
				if o.killed == "" && netOK && ticks%netEveryTick == 0 {
					got := rxKB()
					credit = netCreditMs(got, s.netUsPerMB)
					switch {
					case got > s.netRxKB:
						o.killed = "net"
					case s.strictOut && s.fsizeKB > s.outKB && (fileKB(outPath) > s.outKB || (!s.merge && fileKB(errPath) > s.outKB)):
						// The file limit no longer stops output at the
						// output limit, so watch for it here.
						o.killed = "out"
					}
				}
			}
			if o.killed != "" {
				b.killOwned()
			}
		}
	}
	o.wallMs = int(time.Since(start) / time.Millisecond)
	if netOK {
		o.netRxKB = rxKB()
		o.creditMs = netCreditMs(min(o.netRxKB, s.netRxKB), s.netUsPerMB)
	}

	rec, ok := readReport(repR)
	if !ok {
		// The launcher died without reporting: the program killed it, or
		// took the whole sandbox down. Never guess a result.
		o.err = errors.New("the program interfered with the judge")
		b.killOwned()
		return o
	}
	if rec.exited {
		o.exit = rec.code
	} else {
		o.signal, o.exit = rec.code, -1
	}
	// The launcher's figures are exact for the program and every child it
	// waited for. Sampled figures also see children it never reaped, so
	// they win when they are what tripped a limit.
	o.cpuMs, o.memKB = rec.cpuMs, rec.maxRSSKB
	if o.killed == "cpu" && liveCPU > o.cpuMs {
		o.cpuMs = liveCPU
	}
	switch {
	case s.anon && watch.sampled:
		o.memKB = watch.peakKB
		if o.killed == "mem" && o.memKB <= s.memKB {
			o.memKB = peakKB // stopped by the resident-size backstop
		}
	case s.anon:
		// Too short-lived to sample. Peak resident size is an upper bound
		// and is small for a program that ends this quickly.
	case peakKB > o.memKB:
		o.memKB = peakKB
	}
	if !s.keep {
		b.killOwned()
	}

	var t1, t2 bool
	o.stdout, t1 = readCapped(outPath, s.outKB)
	if !s.merge {
		o.stderr, t2 = readCapped(errPath, s.outKB)
	}
	// Only a test run treats a full output file as an error: there it
	// means the program wanted to write more than it may.
	o.truncated = (t1 || t2) && s.strictOut
	return o
}

// fileKB is a file's size, rounded down, or 0 if it cannot be read.
func fileKB(path string) int {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return int(st.Size() / 1024)
}

// usage sums memory and CPU over the processes the current step owns.
func (b *boxer) usage() (rssKB, cpuMs int) {
	for _, p := range b.owned() {
		rssKB += p.rssKB
		cpuMs += p.cpuMs
	}
	return
}

// owned lists live processes that belong to the step being run: anything
// that did not exist before it, except children of a daemon started by the
// prepare step (a database backend, for example).
func (b *boxer) owned() []procInfo {
	var fresh []procInfo
	byPid := map[int]procInfo{}
	for _, pid := range listPids() {
		if b.baseline[pid] || pid == b.self || pid == b.helper {
			continue
		}
		if p, ok := readProc(pid); ok {
			fresh = append(fresh, p)
			byPid[pid] = p
		}
	}
	out := fresh[:0]
	for _, p := range fresh {
		anc := p.ppid
		for hops := 0; hops < 64; hops++ {
			q, ok := byPid[anc]
			if !ok {
				break
			}
			anc = q.ppid
		}
		// anc is now the first ancestor that predates the step (or the
		// launcher, which is ours).
		daemonChild := b.baseline[anc] && anc != 1 && anc != b.self
		if !daemonChild && p.state != 'Z' {
			out = append(out, p)
		}
	}
	return out
}

func (b *boxer) killOwned() {
	for i := 0; i < 200; i++ {
		ps := b.owned()
		if len(ps) == 0 {
			return
		}
		for _, p := range ps {
			syscall.Kill(p.pid, syscall.SIGKILL)
		}
		time.Sleep(time.Millisecond)
	}
}

func (b *boxer) buildEnv() []string {
	env := map[string]string{
		"PATH":   "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME":   homeDir,
		"TMPDIR": "/tmp",
		"LANG":   "C.UTF-8",
	}
	// The supervisor's own environment is the image's, handed in by the
	// runner when it created the sandbox.
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for k, v := range b.job.Env {
		env[k] = v
	}
	env["HOME"] = homeDir
	for k, v := range b.m.Env {
		env[k] = v
	}
	lim := b.job.Req.Limits
	env["JUDGE_CPU_MS"] = strconv.Itoa(lim.CPUMs)
	env["JUDGE_WALL_MS"] = strconv.Itoa(lim.WallMs)
	env["JUDGE_MEMORY_KB"] = strconv.Itoa(lim.MemoryKB)
	env["JUDGE_SOURCE"] = b.m.SourceFile
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

func writeFiles(m *proto.Manifest, req *proto.RunRequest) error {
	for name, data := range req.Files {
		clean := filepath.Clean("/" + name)[1:] // strips any leading ../
		if clean == "" || clean == m.SourceFile {
			continue
		}
		p := filepath.Join(workDir, clean)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(workDir, m.SourceFile), req.Source, 0o644)
}

func readCapped(path string, capKB int) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	limit := int64(capKB) * 1024
	if limit <= 0 {
		limit = 1 << 20
	}
	st, err := f.Stat()
	if err != nil {
		return nil, false
	}
	n := st.Size()
	if n > limit {
		n = limit
	}
	buf := make([]byte, n)
	got, _ := readFull(f, buf)
	return buf[:got], st.Size() >= limit
}

func readFull(f *os.File, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := f.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

type launchReport struct {
	exited          bool
	code            int // exit code, or signal number when !exited
	cpuMs, maxRSSKB int
}

// readReport parses the single line jexec writes when the program ends.
func readReport(r *os.File) (launchReport, bool) {
	buf := make([]byte, 256)
	n, _ := readFull(r, buf)
	return ParseReport(string(buf[:n]))
}

// ParseReport parses "<exited> <code> <user usec> <sys usec> <maxrss kb>".
func ParseReport(line string) (launchReport, bool) {
	f := strings.Fields(line)
	if len(f) != 5 {
		return launchReport{}, false
	}
	var v [5]int64
	for i, x := range f {
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil || n < 0 {
			return launchReport{}, false
		}
		v[i] = n
	}
	return launchReport{
		exited:   v[0] == 1,
		code:     int(v[1]),
		cpuMs:    int((v[2] + v[3] + 500) / 1000),
		maxRSSKB: int(v[4]),
	}, true
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return strings.TrimSpace(string(b))
}

func pidSet() map[int]bool {
	set := map[int]bool{}
	for _, pid := range listPids() {
		set[pid] = true
	}
	return set
}
