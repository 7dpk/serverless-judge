// The API service: a Judge0-compatible front for the serverless judge.
//
// It validates submissions, stores them in Firestore, hands them to language
// runners and decides the final status by comparing output on this side.
// Expected output never reaches a sandbox.
//
// One binary, two Cloud Run services:
//
//	ROLE=api     public. Serves the REST API. Runs wait=true submissions
//	             itself and publishes the rest to Pub/Sub.
//	ROLE=worker  private. Receives Pub/Sub push deliveries and runs them.
//
// They are separate services so that a backlog of queued work can never use
// up the capacity that answers API callers.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/pubsub/v2"
)

const (
	maxBodyBytes = 32 << 20
	syncWait     = 20 * time.Second // how long wait=true waits at the gate
	syncPatience = 10 * time.Second // then how long it keeps trying for a runner
)

type App struct {
	cfg     *Config
	store   *Store
	langs   *langCache
	runners *Runners
	queue   *pubsub.Publisher // where queued groups are published (API role)
	gates   sync.Map          // per language family: chan struct{}; see gate
	stats   *statsBuf         // analytics counted here, written periodically
	tokens  sync.Map          // temporary token id -> *cachedToken
}

func main() {
	log.SetFlags(0)
	cfg := loadConfig()
	ctx := context.Background()

	fs, err := firestore.NewClientWithDatabase(ctx, cfg.Project, cfg.Database)
	if err != nil {
		log.Fatalf("firestore: %v", err)
	}
	store := &Store{c: fs}
	app := &App{
		cfg:     cfg,
		store:   store,
		langs:   &langCache{st: store},
		runners: newRunners(cfg.RunnerNoAuth),
		stats:   newStatsBuf(),
	}
	if err := app.langs.load(ctx); err != nil {
		log.Printf(`{"severity":"WARNING","message":"language registry not loaded yet: %v"}`, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	if cfg.Role == "worker" {
		// No token check here: this service is deployed without public
		// access, so Cloud Run admits only the push subscription's identity.
		mux.HandleFunc("POST /internal/run", app.handlePush)
	} else {
		ps, err := pubsub.NewClient(ctx, cfg.Project)
		if err != nil {
			log.Fatalf("pubsub: %v", err)
		}
		app.queue = ps.Publisher(cfg.Topic)
		// A message is a few hundred bytes; do not hold it back to batch.
		app.queue.PublishSettings.DelayThreshold = 2 * time.Millisecond
		app.queue.PublishSettings.CountThreshold = 100

		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ui/", http.StatusFound) })
		mux.HandleFunc("GET /ui", handleWeb)
		mux.HandleFunc("GET /ui/", handleWeb)
		mux.HandleFunc("GET /about", app.handleAbout)
		mux.HandleFunc("GET /whoami", app.auth(app.handleWhoAmI))
		mux.HandleFunc("POST /admin/tokens", app.admin(app.handleTokenCreate))
		mux.HandleFunc("GET /admin/tokens", app.admin(app.handleTokenList))
		mux.HandleFunc("DELETE /admin/tokens/{id}", app.admin(app.handleTokenRevoke))
		mux.HandleFunc("GET /admin/stats", app.admin(app.handleStats))
		mux.HandleFunc("GET /statuses", app.auth(app.handleStatuses))
		mux.HandleFunc("GET /languages", app.auth(app.handleLanguages))
		mux.HandleFunc("GET /languages/{id}", app.auth(app.handleLanguage))
		mux.HandleFunc("GET /config_info", app.auth(app.handleConfig))
		mux.HandleFunc("POST /submissions", app.auth(app.handleCreate))
		mux.HandleFunc("GET /submissions/{token}", app.auth(app.handleGet))
		mux.HandleFunc("POST /submissions/batch", app.auth(app.handleCreateBatch))
		mux.HandleFunc("GET /submissions/batch", app.auth(app.handleGetBatch))
	}

	srv := &http.Server{Addr: ":" + env("PORT", "8080"), Handler: mux}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		app.stats.flush(ctx, app.store, true) // last counts before the instance goes
	}()
	log.Printf(`{"message":"%s listening","languages":%d}`, cfg.Role, len(app.langs.all()))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func errorJSON(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func same(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func qbool(r *http.Request, name string) bool {
	return strings.EqualFold(r.URL.Query().Get(name), "true")
}

func (a *App) handleAbout(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"name":        "serverless-judge",
		"description": "Judge0-compatible code execution on Cloud Run sandboxes",
		"version":     env("K_REVISION", "dev"),
	})
}

func (a *App) handleStatuses(w http.ResponseWriter, r *http.Request) {
	out := make([]map[string]any, 0, len(statusNames))
	for id := StInQueue; id <= StMLE; id++ {
		out = append(out, map[string]any{"id": id, "description": statusNames[id]})
	}
	writeJSON(w, 200, out)
}

func (a *App) handleLanguages(w http.ResponseWriter, r *http.Request) {
	list := a.langs.all()
	if list == nil {
		list = []*Language{}
	}
	writeJSON(w, 200, list)
}

func (a *App) handleLanguage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	l := a.langs.get(id)
	if l == nil {
		errorJSON(w, 404, fmt.Sprintf("Language with id %s doesn't exist.", r.PathValue("id")))
		return
	}
	writeJSON(w, 200, map[string]any{"id": l.ID, "name": l.Name, "is_archived": false, "source_file": l.SourceFile})
}

func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	c := a.cfg
	writeJSON(w, 200, map[string]any{
		"enable_wait_result":               true,
		"enable_compiler_options":          true,
		"enable_command_line_arguments":    true,
		"enable_submission_delete":         false,
		"enable_callbacks":                 true,
		"enable_additional_files":          true,
		"enable_batched_submissions":       true,
		"max_submission_batch_size":        c.MaxBatch,
		"cpu_time_limit":                   c.DefaultCPU,
		"max_cpu_time_limit":               c.MaxCPU,
		"max_wall_time_limit":              c.MaxWall,
		"memory_limit":                     c.DefaultMemKB,
		"max_memory_limit":                 c.MaxMemKB,
		"max_file_size":                    c.DefaultFileKB,
		"max_max_file_size":                c.MaxFileKB,
		"max_processes_and_or_threads":     c.DefaultProcs,
		"max_max_processes_and_or_threads": c.MaxProcs,
		"enable_network":                   false,
		"allow_enable_network":             c.AllowNetwork,
		// Not in Judge0: the limits that come with enable_network.
		"network_time_limit":     c.DefaultNetTime,
		"max_network_time_limit": c.MaxNetTime,
		"max_download_size":      c.MaxDownloadKB,
	})
}

func (a *App) handleCreate(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	b64, wait := qbool(r, "base64_encoded"), qbool(r, "wait")
	fields, err := parseFields(r.URL.Query().Get("fields"))
	if err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	var in Input
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&in); err != nil {
		errorJSON(w, 400, "request body is not valid JSON")
		return
	}
	sub, errs := a.buildFor(who(r), &in, b64, time.Now())
	if errs != nil {
		writeJSON(w, 422, errs)
		return
	}
	if !a.spend(who(r), 1) {
		errorJSON(w, 429, "this token has used all of its submissions")
		return
	}
	ctx := r.Context()

	if !wait {
		if err := a.store.Put(ctx, sub); err != nil {
			log.Printf(`{"severity":"ERROR","message":"store: %v"}`, err)
			errorJSON(w, 503, "could not store the submission")
			return
		}
		if lost := a.dispatch(ctx, []group{{Tokens: []string{sub.Token}, LanguageID: sub.LanguageID}}); len(lost) > 0 {
			sub.fail(StInternal, "could not queue the submission", time.Now())
			a.store.Put(ctx, sub)
			errorJSON(w, 503, "queue is unavailable")
			return
		}
		writeJSON(w, 201, map[string]string{"token": sub.Token})
		return
	}

	// Wait in line with everything else for this language, then run.
	leave, ok := a.enter(ctx, a.langs.get(sub.LanguageID), syncWait)
	if !ok {
		errorJSON(w, 503, "queue is full")
		return
	}
	err = a.execute(ctx, []*Submission{sub}, syncPatience)
	leave()
	if err != nil {
		errorJSON(w, 503, "queue is full")
		return
	}
	// Keep the stored copy out of the caller's way: use a context that
	// survives a client hanging up right after the result is computed.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := a.store.Put(saveCtx, sub); err != nil {
		log.Printf(`{"severity":"ERROR","message":"store: %v"}`, err)
	}
	a.callback(saveCtx, sub)
	a.record([]*Submission{sub}, true)
	defer a.stats.flush(saveCtx, a.store, false) // after the response is written
	t := sub.timing
	w.Header().Set("Server-Timing", fmt.Sprintf("total;dur=%d, runner;dur=%d, box;dur=%d, sandbox-wait;dur=%d, warm;desc=%t",
		time.Since(started)/time.Millisecond, t.rpcMs, t.boxMs, t.waitMs, t.warm))
	writeJSON(w, 201, a.render(sub, fields, b64))
}

func (a *App) handleGet(w http.ResponseWriter, r *http.Request) {
	fields, err := parseFields(r.URL.Query().Get("fields"))
	if err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	token := r.PathValue("token")
	sub, err := a.store.Get(r.Context(), token)
	if err == nil && sub.expired(time.Now()) {
		err = errNotFound // past its retention: gone, even if not yet swept
	}
	if errors.Is(err, errNotFound) {
		errorJSON(w, 404, fmt.Sprintf("Submission with token %s doesn't exist.", token))
		return
	}
	if err != nil {
		log.Printf(`{"severity":"ERROR","message":"store: %v"}`, err)
		errorJSON(w, 503, "could not read the submission")
		return
	}
	writeJSON(w, 200, a.render(sub, fields, qbool(r, "base64_encoded")))
}

func (a *App) handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	b64 := qbool(r, "base64_encoded")
	var body struct {
		Submissions []Input `json:"submissions"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&body); err != nil {
		errorJSON(w, 400, "request body is not valid JSON")
		return
	}
	n := len(body.Submissions)
	if n == 0 {
		errorJSON(w, 422, "there should be at least one submission in a batch")
		return
	}
	if n > a.cfg.MaxBatch {
		errorJSON(w, 422, fmt.Sprintf("number of submissions in a batch should be less than or equal to %d", a.cfg.MaxBatch))
		return
	}
	now := time.Now()
	results := make([]any, n)
	var valid []*Submission
	for i := range body.Submissions {
		sub, errs := a.buildFor(who(r), &body.Submissions[i], b64, now)
		if errs != nil {
			results[i] = errs
			continue
		}
		results[i] = map[string]string{"token": sub.Token}
		valid = append(valid, sub)
	}
	ctx := r.Context()
	if !a.spend(who(r), len(valid)) {
		errorJSON(w, 429, "this token does not have enough submissions left for this batch")
		return
	}
	if len(valid) > 0 {
		if err := a.store.PutAll(ctx, valid); err != nil {
			log.Printf(`{"severity":"ERROR","message":"store: %v"}`, err)
			errorJSON(w, 503, "could not store the submissions")
			return
		}
		// One unit of work per program: identical code is compiled once
		// and its inputs run as tests in a single sandbox.
		for _, g := range a.dispatch(ctx, groupTokens(valid, a.cfg.GroupSize)) {
			a.failTokens(ctx, valid, g.Tokens, "could not queue the submission")
		}
	}
	writeJSON(w, 201, results)
}

// groupTokens splits submissions into runner-sized groups of the same
// program, keeping submission order inside each group.
func groupTokens(subs []*Submission, size int) []group {
	var order []string
	by := map[string]*group{}
	for _, s := range subs {
		g, ok := by[s.Group]
		if !ok {
			g = &group{LanguageID: s.LanguageID}
			by[s.Group] = g
			order = append(order, s.Group)
		}
		g.Tokens = append(g.Tokens, s.Token)
	}
	var out []group
	for _, key := range order {
		g := by[key]
		toks := g.Tokens
		for len(toks) > size {
			out = append(out, group{Tokens: toks[:size], LanguageID: g.LanguageID})
			toks = toks[size:]
		}
		out = append(out, group{Tokens: toks, LanguageID: g.LanguageID})
	}
	return out
}

func (a *App) failTokens(ctx context.Context, subs []*Submission, tokens []string, msg string) {
	want := map[string]bool{}
	for _, t := range tokens {
		want[t] = true
	}
	now := time.Now()
	var failed []*Submission
	for _, s := range subs {
		if want[s.Token] {
			s.fail(StInternal, msg, now)
			failed = append(failed, s)
		}
	}
	a.store.PutAll(context.WithoutCancel(ctx), failed)
}

func (a *App) handleGetBatch(w http.ResponseWriter, r *http.Request) {
	fields, err := parseFields(r.URL.Query().Get("fields"))
	if err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	var tokens []string
	for _, t := range strings.Split(r.URL.Query().Get("tokens"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		errorJSON(w, 400, "tokens query parameter is required")
		return
	}
	if len(tokens) > a.cfg.MaxBatch {
		errorJSON(w, 400, fmt.Sprintf("number of submissions in a batch should be less than or equal to %d", a.cfg.MaxBatch))
		return
	}
	subs, err := a.store.GetAll(r.Context(), tokens)
	if err != nil {
		log.Printf(`{"severity":"ERROR","message":"store: %v"}`, err)
		errorJSON(w, 503, "could not read the submissions")
		return
	}
	b64 := qbool(r, "base64_encoded")
	out := make([]any, len(subs))
	now := time.Now()
	for i, s := range subs {
		if s != nil && !s.expired(now) {
			out[i] = a.render(s, fields, b64)
		}
	}
	writeJSON(w, 200, map[string]any{"submissions": out})
}

// record counts finished submissions for the dashboard.
func (a *App) record(subs []*Submission, sync bool) {
	for _, s := range subs {
		name := "unknown"
		if l := a.langs.get(s.LanguageID); l != nil {
			name = l.Lang
		}
		a.stats.record(name, s, sync)
	}
}
