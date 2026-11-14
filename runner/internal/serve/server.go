// Package serve is the HTTP front of a runner container. It accepts one
// program plus its test inputs, hands them to a fresh sandbox, and returns
// the raw results. It holds no credentials and never compares outputs: the
// API service does that.
package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"serverless-judge/runner/proto"
)

const (
	maxRequestBytes = 64 << 20
	maxTests        = 100

	// Budget for steps the caller does not control (see box.go).
	compileBudget = 30 * time.Second
	hookBudget    = 30 * time.Second
)

type Server struct {
	langs    map[string]*proto.Manifest
	pool     *Pool
	env      map[string]string
	instance string
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// Main is the entry point for `judge serve`.
func Main() {
	log.SetFlags(0)
	langDir := getenv("JUDGE_LANG_DIR", "/opt/judge/langs")
	langs, err := loadManifests(langDir)
	if err != nil {
		log.Fatalf("load manifests: %v", err)
	}
	if len(langs) == 0 {
		log.Fatalf("no language manifests in %s", langDir)
	}

	self, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	sandboxBin := getenv("JUDGE_SANDBOX_BIN", "/usr/local/gcp/bin/sandbox")
	if os.Getenv("JUDGE_SANDBOX") == "none" {
		// Local development only. Refuse to run unsandboxed on Cloud Run.
		if os.Getenv("K_SERVICE") != "" {
			log.Fatal("JUDGE_SANDBOX=none is not allowed on Cloud Run")
		}
		sandboxBin = ""
	} else if _, err := os.Stat(sandboxBin); err != nil {
		log.Fatalf("sandbox launcher not found at %s: deploy with --sandbox-launcher", sandboxBin)
	}

	env := imageEnv()
	s := &Server{
		langs:    langs,
		pool:     NewPool(sandboxBin, self, langDir, os.Getenv("JUDGE_SANDBOX_WRITE") == "1", env),
		env:      env,
		instance: randHex(4),
	}
	s.pool.Refill() // first sandbox is created during startup, which is billed anyway

	mux := http.NewServeMux()
	mux.HandleFunc("/run", s.handleRun)
	mux.HandleFunc("/info", s.handleInfo)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })

	srv := &http.Server{Addr: ":" + getenv("PORT", "8080"), Handler: mux}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		s.pool.Close()
	}()
	names := make([]string, 0, len(langs))
	for n := range langs {
		names = append(names, n)
	}
	sort.Strings(names)
	log.Printf("runner %s serving %s", s.instance, strings.Join(names, ", "))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func loadManifests(dir string) (map[string]*proto.Manifest, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := map[string]*proto.Manifest{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var m proto.Manifest
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if m.Name == "" || m.SourceFile == "" || len(m.Run.Cmd) == 0 {
			return nil, fmt.Errorf("%s: name, source_file and run.cmd are required", p)
		}
		out[m.Name] = &m
	}
	return out, nil
}

// imageEnv is the container image's environment (PATH, JAVA_HOME, GOROOT and
// so on) minus anything Cloud Run or this runner added. Sandboxes start with
// an empty environment, so the supervisor needs this to find toolchains.
func imageEnv() map[string]string {
	skip := []string{"K_", "PORT", "JUDGE_", "CLOUD_RUN_", "GOOGLE_", "GCP_", "HOSTNAME", "HOME", "PWD", "SHLVL", "_"}
	out := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		drop := false
		for _, p := range skip {
			if k == p || (strings.HasSuffix(p, "_") && strings.HasPrefix(k, p)) {
				drop = true
				break
			}
		}
		if !drop {
			out[k] = v
		}
	}
	return out
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	list := make([]*proto.Manifest, 0, len(s.langs))
	for _, m := range s.langs {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"instance": s.instance, "languages": list})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	t0 := time.Now()
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req proto.RunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes)).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	m := s.langs[req.Language]
	if m == nil {
		http.Error(w, "unknown language "+req.Language, http.StatusBadRequest)
		return
	}
	if len(req.Tests) == 0 {
		req.Tests = []proto.Test{{}}
	}
	if len(req.Tests) > maxTests {
		http.Error(w, "too many tests in one request", http.StatusBadRequest)
		return
	}
	lim := &req.Limits
	if lim.CPUMs <= 0 {
		lim.CPUMs = 2000
	}
	if lim.WallMs <= 0 {
		lim.WallMs = 2*lim.CPUMs + 1000
	}
	if lim.MemoryKB <= 0 {
		lim.MemoryKB = 256 * 1024
	}
	if lim.OutputKB <= 0 {
		lim.OutputKB = 1024
	}
	if lim.Procs <= 0 {
		lim.Procs = 128
	}

	// Worst case the supervisor can legitimately take, plus slack. Past this
	// the sandbox is destroyed from outside.
	perTest := time.Duration(lim.WallMs) * time.Millisecond
	if m.WallClock {
		perTest = time.Duration(lim.CPUMs) * time.Millisecond
	}
	if m.BeforeRun != nil {
		perTest += hookBudget
	}
	if m.AfterRun != nil {
		perTest += hookBudget
	}
	budget := 10*time.Second + time.Duration(len(req.Tests))*(perTest+time.Second)
	if m.Compile != nil {
		budget += compileBudget
	}
	if m.Prepare != nil {
		budget += hookBudget
	}

	// Commit to the request before running anything: send the status line
	// now. A caller that gets a 200 knows the job started, so a response that
	// then breaks off means this instance died mid-run. A caller that gets
	// anything else (Cloud Run's own 429 or 500 when no instance is free)
	// knows nothing ran and may safely try again.
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	job := &proto.Job{Nonce: randHex(16), Manifest: *m, Req: req, Env: s.env}
	resp, err := s.pool.Run(job, budget)
	if err != nil {
		log.Printf(`{"severity":"ERROR","message":%q,"language":%q}`, err.Error(), req.Language)
		resp = &proto.RunResponse{Error: err.Error()}
	}
	resp.Meta.TotalMs = int(time.Since(t0) / time.Millisecond)
	resp.Meta.Instance = s.instance
	json.NewEncoder(w).Encode(resp)
}
