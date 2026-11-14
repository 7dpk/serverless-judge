package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"serverless-judge/runner/proto"
)

func testApp() *App {
	lc := &langCache{loaded: time.Now(), byID: map[int]*Language{
		71: {ID: 71, Name: "Python", Lang: "python", URL: "http://runner"},
		54: {ID: 54, Name: "C++", Lang: "cpp", URL: "http://runner"},
	}}
	return &App{cfg: &Config{
		DefaultCPU: 2, MaxCPU: 2, MaxWall: 10,
		DefaultMemKB: 256 * 1024, MaxMemKB: 512 * 1024,
		DefaultFileKB: 1024, MaxFileKB: 4096, DefaultProcs: 64, MaxProcs: 128,
		MaxSourceKB: 256, MaxStdinKB: 1024, MaxBatch: 100, GroupSize: 25, TTL: time.Hour,
		AuthTokens: []string{"admin-key"},
	}, langs: lc, stats: newStatsBuf()}
}

func input(t *testing.T, body string) *Input {
	t.Helper()
	var in Input
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatal(err)
	}
	return &in
}

func TestBuildDefaultsAreCodeforcesStyle(t *testing.T) {
	a := testApp()
	s, errs := a.build(input(t, `{"source_code":"print(1)","language_id":71}`), false, time.Now())
	if errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if s.CPUMs != 2000 || s.MemoryKB != 256*1024 || s.WallMs != 5000 {
		t.Errorf("defaults: cpu=%dms mem=%dKB wall=%dms, want 2000 / 262144 / 5000", s.CPUMs, s.MemoryKB, s.WallMs)
	}
	if s.StatusID != StInQueue || s.Token == "" || s.HasExpected {
		t.Errorf("new submission state wrong: %+v", s)
	}
}

func TestBuildEnforcesCaps(t *testing.T) {
	a := testApp()
	cases := []struct {
		name, body, field string
	}{
		{"cpu above 2s", `{"source_code":"x","language_id":71,"cpu_time_limit":2.5}`, "cpu_time_limit"},
		{"memory above 512MB", `{"source_code":"x","language_id":71,"memory_limit":600000}`, "memory_limit"},
		{"zero cpu", `{"source_code":"x","language_id":71,"cpu_time_limit":0}`, "cpu_time_limit"},
		{"unknown language", `{"source_code":"x","language_id":9999}`, "language_id"},
		{"missing language", `{"source_code":"x"}`, "language_id"},
		{"missing source", `{"language_id":71}`, "source_code"},
		{"local callback", `{"source_code":"x","language_id":71,"callback_url":"http://127.0.0.1/x"}`, "callback_url"},
		{"metadata callback", `{"source_code":"x","language_id":71,"callback_url":"http://169.254.169.254/x"}`, "callback_url"},
	}
	for _, c := range cases {
		_, errs := a.build(input(t, c.body), false, time.Now())
		if len(errs[c.field]) == 0 {
			t.Errorf("%s: expected an error on %s, got %v", c.name, c.field, errs)
		}
	}
}

func TestBuildAcceptsLimitsAtTheCap(t *testing.T) {
	a := testApp()
	// Clients send limits as numbers or strings; both must work.
	s, errs := a.build(input(t, `{"source_code":"x","language_id":"54","cpu_time_limit":"2","memory_limit":524288,"wall_time_limit":3}`), false, time.Now())
	if errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if s.CPUMs != 2000 || s.MemoryKB != 524288 || s.WallMs != 3000 || s.LanguageID != 54 {
		t.Errorf("got cpu=%d mem=%d wall=%d lang=%d", s.CPUMs, s.MemoryKB, s.WallMs, s.LanguageID)
	}
}

func TestBuildBase64(t *testing.T) {
	a := testApp()
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	// Ruby-style wrapped base64 must decode too.
	wrapped := enc("print(input())")[:8] + "\n" + enc("print(input())")[8:] + "\n"
	body, _ := json.Marshal(map[string]any{"source_code": wrapped, "language_id": 71, "stdin": enc("héllo\n"), "expected_output": enc("héllo")})
	s, errs := a.build(input(t, string(body)), true, time.Now())
	if errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if string(s.Source) != "print(input())" || string(s.Stdin) != "héllo\n" || !s.HasExpected {
		t.Errorf("decoded source=%q stdin=%q hasExpected=%v", s.Source, s.Stdin, s.HasExpected)
	}
	if _, errs := a.build(input(t, `{"source_code":"@@@","language_id":71}`), true, time.Now()); len(errs["source_code"]) == 0 {
		t.Error("invalid base64 was accepted")
	}
}

func TestNormalizeMatchesJudge0(t *testing.T) {
	same := [][2]string{
		{"1 2 3\n", "1 2 3"},
		{"a  \nb\t\n\n\n", "a\nb"},
		{"x\r\ny\r\n", "x\ny"},
	}
	for _, p := range same {
		if normalize([]byte(p[0])) != normalize([]byte(p[1])) {
			t.Errorf("%q and %q should compare equal", p[0], p[1])
		}
	}
	differ := [][2]string{
		{"1 2 3", "1  2 3"}, // inner whitespace is significant
		{" a", "a"},         // so is leading whitespace
		{"a\n\nb", "a\nb"},  // and blank lines in the middle
	}
	for _, p := range differ {
		if normalize([]byte(p[0])) == normalize([]byte(p[1])) {
			t.Errorf("%q and %q should differ", p[0], p[1])
		}
	}
}

func TestApplyStatusMapping(t *testing.T) {
	cases := []struct {
		name     string
		res      proto.TestResult
		expected string
		has      bool
		want     int
	}{
		{"accepted without expected output", proto.TestResult{Status: proto.StatusOK, Stdout: []byte("5\n")}, "", false, StAccepted},
		{"accepted with trailing newline", proto.TestResult{Status: proto.StatusOK, Stdout: []byte("5\n")}, "5", true, StAccepted},
		{"wrong answer", proto.TestResult{Status: proto.StatusOK, Stdout: []byte("6\n")}, "5", true, StWrongAnswer},
		{"empty output vs expected", proto.TestResult{Status: proto.StatusOK}, "5", true, StWrongAnswer},
		{"time limit", proto.TestResult{Status: proto.StatusTLE}, "5", true, StTLE},
		{"memory limit", proto.TestResult{Status: proto.StatusMLE}, "", false, StMLE},
		{"output limit", proto.TestResult{Status: proto.StatusOLE, Signal: 25}, "", false, StSIGXFSZ},
		{"segfault", proto.TestResult{Status: proto.StatusRE, Signal: 11, ExitCode: -1}, "", false, StSIGSEGV},
		{"fpe", proto.TestResult{Status: proto.StatusRE, Signal: 8, ExitCode: -1}, "", false, StSIGFPE},
		{"abort", proto.TestResult{Status: proto.StatusRE, Signal: 6, ExitCode: -1}, "", false, StSIGABRT},
		{"other signal", proto.TestResult{Status: proto.StatusRE, Signal: 9, ExitCode: -1}, "", false, StOtherRE},
		{"non-zero exit", proto.TestResult{Status: proto.StatusRE, ExitCode: 1}, "", false, StNZEC},
		{"judge failure", proto.TestResult{Status: proto.StatusIE, Message: "boom"}, "", false, StInternal},
		// A correct-looking output must not rescue a failed run.
		{"right output but crashed", proto.TestResult{Status: proto.StatusRE, ExitCode: 1, Stdout: []byte("5")}, "5", true, StNZEC},
	}
	for _, c := range cases {
		s := &Submission{Expected: []byte(c.expected), HasExpected: c.has}
		s.apply(&c.res, time.Now())
		if s.StatusID != c.want {
			t.Errorf("%s: status %d (%s), want %d (%s)", c.name, s.StatusID, statusNames[s.StatusID], c.want, statusNames[c.want])
		}
	}
}

func TestGroupTokens(t *testing.T) {
	subs := []*Submission{
		{Token: "a1", Group: "A", LanguageID: 54}, {Token: "b1", Group: "B", LanguageID: 71}, {Token: "a2", Group: "A", LanguageID: 54},
		{Token: "a3", Group: "A", LanguageID: 54}, {Token: "b2", Group: "B", LanguageID: 71},
	}
	got := groupTokens(subs, 2)
	want := []group{
		{Tokens: []string{"a1", "a2"}, LanguageID: 54}, {Tokens: []string{"a3"}, LanguageID: 54},
		{Tokens: []string{"b1", "b2"}, LanguageID: 71},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groupTokens = %v, want %v", got, want)
	}
}

func TestGroupKeySeparatesPrograms(t *testing.T) {
	base := Submission{LanguageID: 71, Source: []byte("print(1)"), CPUMs: 2000, MemoryKB: 262144}
	sameProgram := base
	sameProgram.Stdin, sameProgram.Expected = []byte("other input"), []byte("other")
	if groupKey(&base) != groupKey(&sameProgram) {
		t.Error("stdin and expected output must not split a group")
	}
	for name, mut := range map[string]func(*Submission){
		"source":   func(s *Submission) { s.Source = []byte("print(2)") },
		"language": func(s *Submission) { s.LanguageID = 54 },
		"memory":   func(s *Submission) { s.MemoryKB = 524288 },
		"options":  func(s *Submission) { s.CompilerOpts = "-O0" },
	} {
		other := base
		mut(&other)
		if groupKey(&base) == groupKey(&other) {
			t.Errorf("a different %s must be a different group", name)
		}
	}
}

func TestRenderNullsAndBase64(t *testing.T) {
	a := testApp()
	s := &Submission{Token: "t", LanguageID: 71, StatusID: StAccepted, Ran: true, Stdout: []byte("hi\n"), TimeMs: 12, UsedKB: 3400}
	plain := a.render(s, defaultFields, false)
	if plain["stdout"] != "hi\n" || plain["stderr"] != nil || plain["compile_output"] != nil || plain["message"] != nil {
		t.Errorf("plain render wrong: %v", plain)
	}
	if plain["time"] != "0.012" || plain["memory"] != 3400 {
		t.Errorf("time/memory wrong: %v %v", plain["time"], plain["memory"])
	}
	if st := plain["status"].(map[string]any); st["id"] != StAccepted || st["description"] != "Accepted" {
		t.Errorf("status wrong: %v", st)
	}
	if got := a.render(s, defaultFields, true)["stdout"]; got != base64.StdEncoding.EncodeToString([]byte("hi\n")) {
		t.Errorf("base64 stdout wrong: %v", got)
	}
	// A queued submission has no time or memory yet.
	q := a.render(&Submission{Token: "q", StatusID: StInQueue}, defaultFields, false)
	if q["time"] != nil || q["memory"] != nil {
		t.Errorf("queued submission should have null time and memory: %v", q)
	}
}

func TestPackRoundTripAndTruncation(t *testing.T) {
	s := &Submission{Token: "t", Source: []byte("src"), Stdin: []byte("in"), Stdout: bytes.Repeat([]byte("out\n"), 1000)}
	got := unpack(pack(s))
	if string(got.Source) != "src" || string(got.Stdin) != "in" || !bytes.Equal(got.Stdout, s.Stdout) {
		t.Error("pack/unpack did not round-trip")
	}
	// Incompressible output larger than a Firestore document must be cut,
	// not rejected, and the verdict must survive.
	noise := make([]byte, 2<<20)
	rand.New(rand.NewSource(1)).Read(noise)
	if len(gz(noise)) < docBudget {
		t.Fatal("test data compresses too well to exercise truncation")
	}
	big := &Submission{Token: "b", StatusID: StAccepted, Source: []byte("src"), Stdout: noise}
	d := pack(big)
	size := len(d.Source) + len(d.Stdin) + len(d.Expected) + len(d.Files) + len(d.Stdout) + len(d.Stderr) + len(d.CompileOutput)
	if size > docBudget {
		t.Errorf("packed document is %d bytes, over the %d budget", size, docBudget)
	}
	if d.StatusID != StAccepted || !strings.Contains(d.Message, "truncated") {
		t.Errorf("truncated document lost its verdict or note: status=%d message=%q", d.StatusID, d.Message)
	}
	if len(big.Stdout) != len(noise) {
		t.Error("pack must not modify the in-memory submission")
	}
}

func TestUnzipRejectsBombs(t *testing.T) {
	if _, err := unzip([]byte("not a zip")); err == nil {
		t.Error("garbage accepted as a zip")
	}
}

func TestUnfinished(t *testing.T) {
	all := []*Submission{
		{Token: "queued", StatusID: StInQueue},
		nil, // token that no longer exists
		{Token: "done", StatusID: StAccepted},
		{Token: "failed", StatusID: StInternal},
		{Token: "queued-2", StatusID: StInQueue},
	}
	var got []string
	for _, s := range unfinished(all) {
		got = append(got, s.Token)
	}
	// Pub/Sub may deliver a message twice: finished work is never redone.
	if want := []string{"queued", "queued-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unfinished = %v, want %v", got, want)
	}
}

// The gate is what keeps more work than there are runners waiting in order
// instead of hammering a busy runner.
func TestGateLimitsAndQueues(t *testing.T) {
	a := testApp()
	py := &Language{ID: 71, URL: "http://runner-python", MaxInstances: 2}
	ctx := context.Background()

	leave1, ok1 := a.enter(ctx, py, time.Second)
	leave2, ok2 := a.enter(ctx, py, time.Second)
	if !ok1 || !ok2 {
		t.Fatal("two places should be free on a gate of width 2")
	}
	start := time.Now()
	if _, ok := a.enter(ctx, py, 60*time.Millisecond); ok {
		t.Fatal("a third group got in while both places were taken")
	} else if waited := time.Since(start); waited < 50*time.Millisecond {
		t.Errorf("gave up after %v, should have waited about 60ms", waited)
	}

	// A waiter gets in as soon as a place is given back.
	got := make(chan bool, 1)
	go func() { _, ok := a.enter(ctx, py, 5*time.Second); got <- ok }()
	time.Sleep(20 * time.Millisecond)
	leave1()
	select {
	case ok := <-got:
		if !ok {
			t.Error("waiter was refused although a place was freed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter was not woken when a place was freed")
	}
	leave2()

	// Another family is not held up by this one being full.
	java := &Language{ID: 62, URL: "http://runner-java", MaxInstances: 1}
	if _, ok := a.enter(ctx, java, 10*time.Millisecond); !ok {
		t.Error("a different runner's gate should be independent")
	}
	// Two languages on one runner (C and C++) share its capacity.
	c, cpp := &Language{ID: 50, URL: "http://runner-gcc", MaxInstances: 1}, &Language{ID: 54, URL: "http://runner-gcc", MaxInstances: 1}
	if _, ok := a.enter(ctx, c, 10*time.Millisecond); !ok {
		t.Fatal("first use of the gcc gate should succeed")
	}
	if _, ok := a.enter(ctx, cpp, 10*time.Millisecond); ok {
		t.Error("C++ got a place while C held the only gcc instance")
	}
}

// runnerStub serves /run the way Cloud Run or a runner would in each case.
func runnerStub(t *testing.T, mode string, hits *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		switch mode {
		case "ok":
			w.WriteHeader(200)
			w.Write([]byte(`{"tests":[{"status":"ok"}],"meta":{}}`))
		case "no-instance-500": // Cloud Run: aborted waiting for an instance
			w.WriteHeader(500)
			w.Write([]byte("<html><h1>Error: Server Error</h1></html>"))
		case "no-instance-429":
			w.WriteHeader(429)
		case "died-mid-run": // the runner said 200, then the instance vanished
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		case "bad-request":
			w.WriteHeader(400)
			w.Write([]byte("unknown language"))
		}
	}))
}

func TestRunnerFailureClassification(t *testing.T) {
	cases := []struct {
		mode     string
		wantErr  error // nil: success, or an error that is neither busy nor crashed
		wantHits int   // -1: at least 2
		hardFail bool
	}{
		{mode: "ok", wantHits: 1},
		// Nothing ran, so it is retried for as long as patience allows.
		{mode: "no-instance-500", wantErr: errBusy, wantHits: -1},
		{mode: "no-instance-429", wantErr: errBusy, wantHits: -1},
		// The job started and killed its instance: once more, then stop.
		{mode: "died-mid-run", wantErr: errCrashed, wantHits: 2},
		{mode: "bad-request", wantHits: 1, hardFail: true},
	}
	for _, c := range cases {
		hits := 0
		srv := runnerStub(t, c.mode, &hits)
		_, err := newRunners(true).Run(context.Background(), srv.URL, &proto.RunRequest{Language: "x"}, 400*time.Millisecond)
		srv.Close()
		switch {
		case c.hardFail:
			if err == nil || errors.Is(err, errBusy) || errors.Is(err, errCrashed) {
				t.Errorf("%s: want a plain error, got %v", c.mode, err)
			}
		case c.wantErr == nil:
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.mode, err)
			}
		case !errors.Is(err, c.wantErr):
			t.Errorf("%s: got %v, want %v", c.mode, err, c.wantErr)
		}
		if (c.wantHits == -1 && hits < 2) || (c.wantHits > 0 && hits != c.wantHits) {
			t.Errorf("%s: runner was called %d times, want %d (-1 means several)", c.mode, hits, c.wantHits)
		}
	}
}

// A delivery the worker cannot make sense of must be acknowledged, or
// Pub/Sub would redeliver it for ever.
func TestHandlePushAcknowledgesGarbage(t *testing.T) {
	a := testApp()
	for _, body := range []string{"not json", `{}`, `{"message":{"data":"bm90IGpzb24="}}`, `{"message":{"data":"eyJ0b2tlbnMiOltdfQ=="}}`} {
		rec := httptest.NewRecorder()
		a.handlePush(rec, httptest.NewRequest("POST", "/internal/run", strings.NewReader(body))) // a.store is nil: reaching it would panic
		if rec.Code != http.StatusNoContent {
			t.Errorf("body %q answered %d, want 204", body, rec.Code)
		}
	}
}

// A full gate makes the delivery wait, and a wait that runs out answers 503
// so Pub/Sub tries again later, without having loaded or changed anything.
func TestHandlePushWaitsAtTheGate(t *testing.T) {
	a := testApp()
	py := a.langs.get(71)
	py.MaxInstances = 1
	leave, ok := a.enter(context.Background(), py, time.Second)
	if !ok {
		t.Fatal("could not take the only place")
	}
	defer leave()

	env := map[string]any{"message": map[string]any{
		"data": []byte(`{"tokens":["t1"],"language_id":71}`), "publishTime": time.Now().Format(time.RFC3339Nano),
	}}
	body, _ := json.Marshal(env)
	start := time.Now()
	rec := httptest.NewRecorder()
	// handlePush detaches from the request context on purpose, so the test
	// shortens the gate wait itself rather than cancelling the request.
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.handlePushWith(rec, httptest.NewRequest("POST", "/internal/run", bytes.NewReader(body)), 60*time.Millisecond)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery did not give up when its wait ran out")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("answered %d, want 503", rec.Code)
	}
	if waited := time.Since(start); waited < 50*time.Millisecond {
		t.Errorf("gave up after %v without waiting", waited)
	}
}

func TestLatBucket(t *testing.T) {
	cases := map[int]int{0: 0, 25: 0, 26: 1, 100: 2, 101: 3, 3000: 7, 3001: 8, 300000: 13, 300001: 14, 9999999: 14}
	for ms, want := range cases {
		if got := latBucket(ms); got != want {
			t.Errorf("latBucket(%d) = %d, want %d", ms, got, want)
		}
	}
}

// What the dashboard shows comes only from these counters, so they have to
// add up: every finished submission counted once, in the hour it finished.
func TestStatsRecord(t *testing.T) {
	b := newStatsBuf()
	at := time.Date(2026, 11, 14, 14, 59, 58, 0, time.UTC)
	mk := func(status int, latency time.Duration, ran bool, cpuMs, memKB int, finished time.Time) *Submission {
		return &Submission{StatusID: status, CreatedAt: finished.Add(-latency), FinishedAt: finished, Ran: ran, TimeMs: cpuMs, UsedKB: memKB}
	}
	b.record("python", mk(StAccepted, 80*time.Millisecond, true, 30, 5000, at), true)
	b.record("python", mk(StWrongAnswer, 2*time.Second, true, 50, 7000, at), false)
	b.record("python", mk(StCompileError, 400*time.Millisecond, false, 0, 0, at), false)
	b.record("cpp", mk(StAccepted, time.Second, true, 10, 2000, at), false)
	b.record("python", mk(StAccepted, 90*time.Millisecond, true, 20, 4000, at.Add(5*time.Second)), true) // next hour
	b.record("python", &Submission{StatusID: StInQueue}, false)                                          // unfinished: not counted

	py := b.hours["2026111414"]["python"]
	if py == nil || py.n != 3 || py.ran != 2 || py.sync != 1 || py.cpuMs != 80 || py.memKB != 12000 {
		t.Fatalf("python 14:00 tally wrong: %+v", py)
	}
	if py.status[StAccepted] != 1 || py.status[StWrongAnswer] != 1 || py.status[StCompileError] != 1 {
		t.Errorf("verdict counts wrong: %v", py.status)
	}
	if py.hist[latBucket(80)] != 1 || py.hist[latBucket(2000)] != 1 || py.hist[latBucket(400)] != 1 {
		t.Errorf("latency histogram wrong: %v", py.hist)
	}
	var sum int64
	for _, n := range py.hist {
		sum += n
	}
	if sum != py.n {
		t.Errorf("histogram holds %d entries for %d submissions", sum, py.n)
	}
	if b.hours["2026111414"]["cpp"].n != 1 || b.hours["2026111415"]["python"].n != 1 {
		t.Errorf("language or hour split wrong: %v", b.hours)
	}
	if len(py.counters()["s"].(map[string]any)) != 3 {
		t.Errorf("counters should carry one entry per verdict seen")
	}
}

func tempPrincipal(max, used int64, expires time.Time) principal {
	c := &cachedToken{exists: true, fetched: time.Now(), tok: TempToken{Max: max, Used: used, ExpiresAt: expires}}
	return principal{tokenID: "id", entry: c}
}

// A temporary token must stop at its cap, counting what this instance has
// accepted since it last read the stored figure.
func TestSpendEnforcesTheCap(t *testing.T) {
	a := testApp()
	p := tempPrincipal(5, 2, time.Now().Add(time.Hour))
	if !a.spend(p, 2) {
		t.Fatal("2 more on a token with 3 left was refused")
	}
	if a.spend(p, 2) {
		t.Error("a batch of 2 was accepted with only 1 left")
	}
	if !a.spend(p, 1) {
		t.Error("the last submission was refused")
	}
	if a.spend(p, 1) {
		t.Error("a submission past the cap was accepted")
	}
	if got := a.stats.tokens["id"]; got != 3 {
		t.Errorf("charged %d submissions to the token, want 3", got)
	}
	if !a.spend(principal{admin: true}, 1_000_000) {
		t.Error("an admin key has no cap")
	}
}

func TestGuard(t *testing.T) {
	a := testApp()
	reached := false
	call := func(h http.HandlerFunc, key string) int {
		reached = false
		req := httptest.NewRequest("GET", "/x", nil)
		if key != "" {
			req.Header.Set("X-Auth-Token", key)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code
	}
	next := func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(200) }

	if code := call(a.auth(next), ""); code != 401 || reached {
		t.Errorf("no key: %d reached=%v, want 401", code, reached)
	}
	if code := call(a.auth(next), "wrong"); code != 401 || reached {
		t.Errorf("wrong key: %d reached=%v, want 401", code, reached)
	}
	if code := call(a.admin(next), "admin-key"); code != 200 || !reached {
		t.Errorf("admin key on an admin route: %d reached=%v, want 200", code, reached)
	}

	// Seed the cache as if these temporary tokens had just been read.
	live, dead := tempPrefix+"0123456789abcdef-live", tempPrefix+"0123456789abcdef-dead"
	a.tokens.Store(tokenID(live), &cachedToken{exists: true, fetched: time.Now(), tok: TempToken{Max: 10, ExpiresAt: time.Now().Add(time.Hour)}})
	a.tokens.Store(tokenID(dead), &cachedToken{exists: true, fetched: time.Now(), tok: TempToken{Max: 10, ExpiresAt: time.Now().Add(-time.Minute)}})
	a.tokens.Store(tokenID(tempPrefix+"0123456789abcdef-none"), &cachedToken{exists: false, fetched: time.Now()})

	if code := call(a.auth(next), live); code != 200 || !reached {
		t.Errorf("live temporary token: %d reached=%v, want 200", code, reached)
	}
	// The whole point of a temporary token: it cannot mint tokens or read analytics.
	if code := call(a.admin(next), live); code != 403 || reached {
		t.Errorf("temporary token on an admin route: %d reached=%v, want 403", code, reached)
	}
	if code := call(a.auth(next), dead); code != 401 || reached {
		t.Errorf("expired temporary token: %d reached=%v, want 401", code, reached)
	}
	if code := call(a.auth(next), tempPrefix+"0123456789abcdef-none"); code != 401 || reached {
		t.Errorf("unknown temporary token: %d reached=%v, want 401", code, reached)
	}
}

func TestSubmissionExpiry(t *testing.T) {
	now := time.Now()
	if (&Submission{ExpireAt: now.Add(time.Minute)}).expired(now) {
		t.Error("a submission inside its retention read as expired")
	}
	if !(&Submission{ExpireAt: now.Add(-time.Second)}).expired(now) {
		t.Error("a submission past its retention must read as gone")
	}
}

func TestWebFiles(t *testing.T) {
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handleWeb(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	for path, want := range map[string]string{
		"/ui/": "Judge playground", "/ui/admin": "Judge admin", "/ui/app.js": "use strict", "/ui/style.css": "--series-1",
	} {
		rec := get(path)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: status %d, expected body to contain %q", path, rec.Code, want)
		}
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
			t.Errorf("%s: weak or missing content security policy: %q", path, csp)
		}
	}
	// Nothing outside the web folder is reachable.
	for _, path := range []string{"/ui/../main.go", "/ui/%2e%2e/main.go", "/ui/nope.js"} {
		if rec := get(path); rec.Code == 200 {
			t.Errorf("%s was served", path)
		}
	}
	// The pages must not pull anything from another site.
	for _, f := range []string{"index.html", "admin.html", "app.js", "admin.js", "samples.js", "style.css"} {
		data, err := webFiles.ReadFile("web/" + f)
		if err != nil {
			t.Fatalf("%s is not embedded: %v", f, err)
		}
		if bytes.Contains(data, []byte("https://")) || bytes.Contains(data, []byte("//cdn")) {
			t.Errorf("%s refers to an external URL", f)
		}
	}
}

// A language registered after the cache was loaded is found on first use,
// and unknown ids cannot force a registry read on every request.
func TestLanguageMissRechecksRegistry(t *testing.T) {
	rows := []Language{{ID: 71, Name: "Python", URL: "https://runner-python"}}
	reads := 0
	lc := &langCache{fetch: func(context.Context) ([]Language, error) {
		reads++
		return append([]Language(nil), rows...), nil
	}}
	if err := lc.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lc.get(71) == nil || reads != 1 {
		t.Fatalf("known language: reads=%d", reads)
	}

	// Just loaded: a miss is believed without another read.
	if lc.get(999) != nil || reads != 1 {
		t.Fatalf("miss on a fresh cache should not reload: reads=%d", reads)
	}

	// The instance sat idle; meanwhile a language was registered.
	rows = append(rows, Language{ID: 54, Name: "C++", URL: "https://runner-gcc"})
	lc.mu.Lock()
	lc.loaded = time.Now().Add(-10 * time.Second) // old enough to recheck, young enough for no background refresh
	lc.mu.Unlock()
	if l := lc.get(54); l == nil || l.URL != "https://runner-gcc" {
		t.Fatalf("new language not found after a miss: %+v", l)
	}
	if reads != 2 {
		t.Fatalf("want one reload, got reads=%d", reads)
	}

	// Nonsense ids right after that reload do not read again.
	for i := 0; i < 50; i++ {
		lc.get(100000 + i)
	}
	if reads != 2 {
		t.Fatalf("unknown ids forced %d extra reads", reads-2)
	}
}
