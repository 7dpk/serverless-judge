// loadtest drives a deployed judge at a steady rate and checks every verdict.
//
//	JUDGE_URL=https://... JUDGE_KEY=... go run ./tests/loadtest -n 100000 -duration 10m
//
// Every submission is a different program, so n submissions mean n sandboxes.
// About 5% are built to fail in a known way (wrong answer, compile error,
// crash, time limit, memory limit); the test passes only if each one gets
// exactly the verdict it should. Latency is measured on the server's clock,
// from created_at to finished_at, so the client's network does not count.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	stAccepted, stWrong, stTLE, stCompile, stSegv, stNZEC, stInternal, stMLE = 3, 4, 5, 6, 7, 11, 13, 15
	inputN                                                                   = 1000 // loop bound fed on stdin
	sqlRows                                                                  = 100
)

type lang struct {
	name     string
	id       int
	weight   int
	template string // %d is a per-submission constant
	compiled bool
	comment  string
}

var langs = []lang{
	{"python", 71, 30, "n = int(input())\nprint(sum(i * %d for i in range(n)))\n", false, "#"},
	{"cpp", 54, 15, "#include <bits/stdc++.h>\nint main(){ long long n, s = 0; std::cin >> n; for (long long i = 0; i < n; i++) s += i * %dLL; std::cout << s << \"\\n\"; }\n", true, "//"},
	{"javascript", 63, 12, "const n = Number(require(\"fs\").readFileSync(0, \"utf8\"));\nlet s = 0;\nfor (let i = 0; i < n; i++) s += i * %d;\nconsole.log(s);\n", false, "//"},
	{"java", 62, 12, "import java.util.*;\npublic class Main { public static void main(String[] a) { long n = new Scanner(System.in).nextLong(), s = 0; for (long i = 0; i < n; i++) s += i * %dL; System.out.println(s); } }\n", true, "//"},
	{"c", 50, 6, "#include <stdio.h>\nint main(){ long long n, s = 0; scanf(\"%%lld\", &n); for (long long i = 0; i < n; i++) s += i * %dLL; printf(\"%%lld\\n\", s); return 0; }\n", true, "//"},
	{"go", 60, 5, "package main\nimport \"fmt\"\nfunc main() { var n, s int64; fmt.Scan(&n); for i := int64(0); i < n; i++ { s += i * %d }; fmt.Println(s) }\n", true, "//"},
	{"csharp", 51, 5, "using System;\nclass P { static void Main() { long n = long.Parse(Console.ReadLine()), s = 0; for (long i = 0; i < n; i++) s += i * %dL; Console.WriteLine(s); } }\n", true, "//"},
	{"typescript", 74, 4, "import * as fs from \"fs\";\nconst n: number = Number(fs.readFileSync(0, \"utf8\"));\nlet s: number = 0;\nfor (let i = 0; i < n; i++) s += i * %d;\nconsole.log(s);\n", true, "//"},
	{"php", 68, 4, "<?php\n$n = (int)trim(fgets(STDIN)); $s = 0;\nfor ($i = 0; $i < $n; $i++) $s += $i * %d;\necho $s, \"\\n\";\n", false, "//"},
	{"rust", 73, 3, "use std::io::*;\nfn main() { let mut t = String::new(); stdin().read_line(&mut t).unwrap(); let n: i64 = t.trim().parse().unwrap(); let mut s: i64 = 0; for i in 0..n { s += i * %d; } println!(\"{}\", s); }\n", true, "//"},
	{"postgresql", 201, 2, "SELECT SUM(i * %d) AS s FROM t;\n", false, "--"},
	{"mysql", 202, 2, "SELECT SUM(i * %d) AS s FROM t;\n", false, "--"},
}

// Programs that fail on purpose. Each gets a unique comment appended so
// identical faults in one batch are not folded into a single run.
var (
	tleProgs = map[string]string{
		"python":     "while True:\n    pass\n",
		"cpp":        "int main(){ volatile unsigned long x = 0; for(;;) x++; }\n",
		"java":       "public class Main { public static void main(String[] a) { long x = 0; while (true) x++; } }\n",
		"javascript": "while (true) {}\n",
	}
	mleProgs = map[string]string{
		"python": "x = bytearray(400 * 1024 * 1024)\nprint(len(x))\n",
		"cpp":    "#include <bits/stdc++.h>\nint main(){ std::vector<char*> v; for(int i=0;i<400;i++){ char* p=(char*)malloc(1<<20); memset(p,1,1<<20); v.push_back(p);} puts(\"ok\"); }\n",
	}
	crashProgs = map[string][2]any{ // program, expected status
		"python": {"raise SystemExit(3)\n", stNZEC},
		"cpp":    {"int main(){ int* p = nullptr; *p = 1; }\n", stSegv},
		"java":   {"public class Main { public static void main(String[] a) { throw new RuntimeException(\"boom\"); } }\n", stNZEC},
	}
)

type sub struct {
	lang     string
	kind     string // ok, wrong, compile, crash, tle, mle
	want     int
	body     map[string]any
	token    string
	sentAt   time.Time
	got      int
	created  time.Time
	finished time.Time
	execMs   float64
	message  string
	nextPoll time.Time
	polls    int
}

func sqlFixture() string {
	var b strings.Builder
	b.WriteString("CREATE TABLE t (i INT);\nINSERT INTO t VALUES ")
	for i := 0; i < sqlRows; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "(%d)", i)
	}
	b.WriteString(";\n")
	return b.String()
}

func build(k int, rng *rand.Rand, pick []int) *sub {
	l := langs[pick[rng.Intn(len(pick))]]
	s := &sub{lang: l.name, kind: "ok", want: stAccepted}
	isSQL := l.name == "postgresql" || l.name == "mysql"
	src := fmt.Sprintf(l.template, k)
	stdin, expected := fmt.Sprint(inputN), fmt.Sprint(int64(k)*int64(inputN)*int64(inputN-1)/2)
	if isSQL {
		stdin, expected = sqlFixture(), fmt.Sprintf("s\n%d\n", int64(k)*int64(sqlRows)*int64(sqlRows-1)/2)
	}

	tag := fmt.Sprintf("%s load test %d\n", l.comment, k)
	switch r := rng.Float64(); {
	case r < 0.020:
		s.kind, s.want = "wrong", stWrong
		expected = "definitely not the answer"
	case r < 0.030 && l.compiled:
		s.kind, s.want = "compile", stCompile
		src = "this is not a program @@ %% {{\n" + src
	case r < 0.040 && crashProgs[l.name][0] != nil:
		s.kind, s.want = "crash", crashProgs[l.name][1].(int)
		src, expected = crashProgs[l.name][0].(string)+tag, ""
	case r < 0.045 && tleProgs[l.name] != "":
		s.kind, s.want = "tle", stTLE
		src, expected = tleProgs[l.name]+tag, ""
	case r < 0.050 && mleProgs[l.name] != "":
		s.kind, s.want = "mle", stMLE
		src, expected = mleProgs[l.name]+tag, ""
	}
	s.body = map[string]any{"language_id": l.id, "source_code": src, "stdin": stdin}
	if expected != "" {
		s.body["expected_output"] = expected
	}
	return s
}

type client struct {
	url, key string
	http     *http.Client
}

func (c *client) do(method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.url+path, rd)
	req.Header.Set("X-Auth-Token", c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

func pct(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	i := int(float64(len(v)-1) * p)
	return v[i]
}

func main() {
	n := flag.Int("n", 1000, "number of submissions")
	duration := flag.Duration("duration", time.Minute, "time over which to submit them")
	batch := flag.Int("batch", 20, "submissions per batch request")
	grace := flag.Duration("grace", 8*time.Minute, "how long to wait for stragglers after the last submit")
	seed := flag.Int64("seed", 1, "random seed")
	out := flag.String("out", "", "write per-submission results to this JSON file")
	only := flag.String("langs", "", "comma-separated languages to use (default: all, weighted)")
	flag.Parse()

	c := &client{url: strings.TrimRight(os.Getenv("JUDGE_URL"), "/"), key: os.Getenv("JUDGE_KEY"),
		http: &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{
			ForceAttemptHTTP2: true, MaxIdleConnsPerHost: 256, IdleConnTimeout: 90 * time.Second}}}
	if c.url == "" || c.key == "" {
		fmt.Fprintln(os.Stderr, "set JUDGE_URL and JUDGE_KEY")
		os.Exit(2)
	}

	var pick []int
	for i, l := range langs {
		if *only != "" && !strings.Contains(","+*only+",", ","+l.name+",") {
			continue
		}
		for w := 0; w < l.weight; w++ {
			pick = append(pick, i)
		}
	}
	if len(pick) == 0 {
		fmt.Fprintln(os.Stderr, "no language matches -langs")
		os.Exit(2)
	}
	rng := rand.New(rand.NewSource(*seed))
	subs := make([]*sub, *n)
	for i := range subs {
		subs[i] = build(i+1, rng, pick)
	}

	var (
		mu                                 sync.Mutex
		submitErrs, pollErrs, sent, landed atomic.Int64
		errSamples                         []string
	)
	note := func(s string) {
		mu.Lock()
		if len(errSamples) < 12 {
			errSamples = append(errSamples, s)
		}
		mu.Unlock()
	}

	start := time.Now()
	batches := (*n + *batch - 1) / *batch
	interval := *duration / time.Duration(batches)
	fmt.Printf("submitting %d programs in %d batches of %d over %s (%.0f/s)\n", *n, batches, *batch, *duration, float64(*n)/duration.Seconds())

	// Sender: one batch per tick, never waiting for the previous one.
	var sendWG sync.WaitGroup
	go func() {
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for b := 0; b < batches; b++ {
			lo, hi := b**batch, min((b+1)**batch, *n)
			sendWG.Add(1)
			go func(group []*sub) {
				defer sendWG.Done()
				bodies := make([]map[string]any, len(group))
				for i, s := range group {
					bodies[i] = s.body
				}
				var data []byte
				var code int
				var err error
				for attempt := 0; attempt < 3; attempt++ {
					code, data, err = c.do("POST", "/submissions/batch", map[string]any{"submissions": bodies})
					if err == nil && code == 201 {
						break
					}
					time.Sleep(time.Duration(attempt+1) * time.Second)
				}
				sent.Add(int64(len(group)))
				var res []map[string]any
				if err != nil || code != 201 || json.Unmarshal(data, &res) != nil || len(res) != len(group) {
					submitErrs.Add(int64(len(group)))
					note(fmt.Sprintf("submit: HTTP %d err=%v body=%.120s", code, err, data))
					return
				}
				now := time.Now()
				mu.Lock()
				for i, s := range group {
					s.body = nil
					if tok, _ := res[i]["token"].(string); tok != "" {
						s.token, s.sentAt, s.nextPoll = tok, now, now.Add(1500*time.Millisecond)
					} else {
						submitErrs.Add(1)
					}
				}
				mu.Unlock()
			}(subs[lo:hi])
			<-tick.C
		}
		sendWG.Wait()
	}()

	// Poller: ask for due tokens 100 at a time.
	byToken := map[string]*sub{}
	sem := make(chan struct{}, 32)
	var pollWG sync.WaitGroup
	lastReport := time.Now()
	deadline := start.Add(*duration + *grace)
	for {
		now := time.Now()
		var due []*sub
		pending := 0
		mu.Lock()
		for _, s := range subs {
			if s.token == "" || s.got > 2 {
				continue
			}
			pending++
			if _, ok := byToken[s.token]; !ok {
				byToken[s.token] = s
			}
			if !s.nextPoll.After(now) {
				due = append(due, s)
				s.polls++
				s.nextPoll = now.Add(time.Duration(min(1+s.polls, 6)) * time.Second)
			}
		}
		mu.Unlock()

		for i := 0; i < len(due); i += 100 {
			chunk := due[i:min(i+100, len(due))]
			toks := make([]string, len(chunk))
			for j, s := range chunk {
				toks[j] = s.token
			}
			pollWG.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; pollWG.Done() }()
				code, data, err := c.do("GET", "/submissions/batch?fields=token,status_id,created_at,finished_at,time,message&tokens="+strings.Join(toks, ","), nil)
				var res struct {
					Submissions []struct {
						Token      string  `json:"token"`
						StatusID   int     `json:"status_id"`
						CreatedAt  string  `json:"created_at"`
						FinishedAt string  `json:"finished_at"`
						Time       *string `json:"time"`
						Message    *string `json:"message"`
					} `json:"submissions"`
				}
				if err != nil || code != 200 || json.Unmarshal(data, &res) != nil {
					pollErrs.Add(1)
					note(fmt.Sprintf("poll: HTTP %d err=%v body=%.120s", code, err, data))
					return
				}
				mu.Lock()
				for _, r := range res.Submissions {
					s := byToken[r.Token]
					if s == nil || r.StatusID <= 2 || s.got > 2 {
						continue
					}
					s.got = r.StatusID
					s.created, _ = time.Parse("2006-01-02T15:04:05.000Z", r.CreatedAt)
					s.finished, _ = time.Parse("2006-01-02T15:04:05.000Z", r.FinishedAt)
					if r.Time != nil {
						fmt.Sscan(*r.Time, &s.execMs)
						s.execMs *= 1000
					}
					if r.Message != nil {
						s.message = *r.Message
					}
					landed.Add(1)
				}
				mu.Unlock()
			}()
		}

		if time.Since(lastReport) >= 15*time.Second {
			lastReport = time.Now()
			fmt.Printf("  t+%4.0fs  sent %6d  finished %6d  in flight %5d  submit errors %d  poll errors %d\n",
				time.Since(start).Seconds(), sent.Load(), landed.Load(), pending, submitErrs.Load(), pollErrs.Load())
		}
		allSent := sent.Load() >= int64(*n)
		if allSent && pending == 0 && time.Since(start) > 2*time.Second {
			break
		}
		if time.Now().After(deadline) {
			fmt.Println("  gave up waiting for stragglers")
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	pollWG.Wait()
	elapsed := time.Since(start)

	// ---- report ----
	type agg struct {
		n, right int
		lat      []float64
		exec     []float64
	}
	byLang, byKind := map[string]*agg{}, map[string]*agg{}
	all := &agg{}
	mismatch := map[string]int{}
	var samples []string
	unfinished, noToken := 0, 0
	finishedPer10s := map[int]int{}
	var firstCreated, lastFinished time.Time
	for _, s := range subs {
		if s.token == "" || s.got <= 2 {
			continue
		}
		if firstCreated.IsZero() || s.created.Before(firstCreated) {
			firstCreated = s.created
		}
		if s.finished.After(lastFinished) {
			lastFinished = s.finished
		}
	}
	for _, s := range subs {
		if s.token == "" {
			noToken++
			continue
		}
		for key, m := range map[string]map[string]*agg{s.lang: byLang, s.kind: byKind} {
			if m[key] == nil {
				m[key] = &agg{}
			}
		}
		if s.got <= 2 {
			unfinished++
			byLang[s.lang].n++
			byKind[s.kind].n++
			all.n++
			continue
		}
		lat := s.finished.Sub(s.created).Seconds()
		finishedPer10s[int(s.finished.Sub(firstCreated).Seconds())/10]++
		for _, a := range []*agg{all, byLang[s.lang], byKind[s.kind]} {
			a.n++
			a.lat = append(a.lat, lat)
			if s.execMs > 0 {
				a.exec = append(a.exec, s.execMs)
			}
			if s.got == s.want {
				a.right++
			}
		}
		if s.got != s.want {
			key := fmt.Sprintf("%s/%s: wanted %d, got %d", s.lang, s.kind, s.want, s.got)
			mismatch[key]++
			if len(samples) < 10 {
				samples = append(samples, fmt.Sprintf("%s token=%s message=%.140q", key, s.token, s.message))
			}
		}
	}
	line := func(name string, a *agg) {
		sort.Float64s(a.lat)
		fmt.Printf("  %-12s %7d  %7d  %6.2f%%  %7.2f %7.2f %7.2f %7.2f\n", name, a.n, a.right,
			100*float64(a.right)/float64(max(a.n, 1)), pct(a.lat, 0.50), pct(a.lat, 0.90), pct(a.lat, 0.99), pct(a.lat, 1))
	}
	fmt.Printf("\nfinished in %s. Latency is created_at to finished_at on the server, in seconds.\n", elapsed.Round(time.Second))
	fmt.Printf("  %-12s %7s  %7s  %7s  %7s %7s %7s %7s\n", "", "count", "correct", "", "p50", "p90", "p99", "max")
	names := make([]string, 0, len(byLang))
	for k := range byLang {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool { return byLang[names[i]].n > byLang[names[j]].n })
	for _, k := range names {
		line(k, byLang[k])
	}
	fmt.Println("  by intended outcome:")
	for _, k := range []string{"ok", "wrong", "compile", "crash", "tle", "mle"} {
		if byKind[k] != nil {
			line(k, byKind[k])
		}
	}
	line("ALL", all)

	peak, peakAt := 0, 0
	for b, v := range finishedPer10s {
		if v > peak {
			peak, peakAt = v, b
		}
	}
	fmt.Printf("\n  peak throughput: %.0f finished/s (10 s window at t+%ds)\n", float64(peak)/10, peakAt*10)
	if span := lastFinished.Sub(firstCreated).Seconds(); span > 0 {
		fmt.Printf("  sustained throughput: %.1f finished/s over the %.0f s from first submit to last result\n", float64(len(all.lat))/span, span)
	}
	fmt.Printf("  never got a token: %d   never finished: %d   submit errors: %d   poll errors: %d\n", noToken, unfinished, submitErrs.Load(), pollErrs.Load())
	if len(mismatch) > 0 {
		fmt.Println("  wrong verdicts:")
		keys := make([]string, 0, len(mismatch))
		for k := range mismatch {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("    %5d  %s\n", mismatch[k], k)
		}
		for _, s := range samples {
			fmt.Println("    e.g.", s)
		}
	}
	for _, s := range errSamples {
		fmt.Println("  error sample:", s)
	}
	if *out != "" {
		type row struct {
			Lang, Kind string
			Want, Got  int
			LatencyS   float64
			ExecMs     float64
			FinishedS  float64
			Token      string
		}
		rows := make([]row, 0, len(subs))
		for _, s := range subs {
			r := row{Lang: s.lang, Kind: s.kind, Want: s.want, Got: s.got, ExecMs: s.execMs, Token: s.token}
			if s.got > 2 {
				r.LatencyS, r.FinishedS = s.finished.Sub(s.created).Seconds(), s.finished.Sub(firstCreated).Seconds()
			}
			rows = append(rows, r)
		}
		data, _ := json.Marshal(rows)
		os.WriteFile(*out, data, 0o644)
	}
	ok := noToken == 0 && unfinished == 0 && all.right == all.n && all.n == *n
	fmt.Println("\nRESULT:", map[bool]string{true: "PASS, every submission finished with the verdict it should have", false: "FAIL"}[ok])
	if !ok {
		os.Exit(1)
	}
}
