package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"serverless-judge/runner/proto"
)

// errBusy means the job never started: no runner instance was free, or none
// could be created. Nothing ran, so the caller may try again later.
var errBusy = errors.New("runner has no capacity")

// errCrashed means the runner accepted the job and then died, most likely
// because this very submission exhausted it. Retrying would take down one
// instance after another, so it is tried once more at most.
//
// The two cannot be told apart by status code: Cloud Run answers "no
// instance available" with 429 or 500 depending on the cause. So the runner
// sends its 200 status line before it runs anything, and the difference is
// whether that arrived.
var errCrashed = errors.New("runner instance failed while running this submission")

// Runners calls runner services, authenticating as this service's identity.
type Runners struct {
	http   *http.Client
	noAuth bool

	mu     sync.Mutex
	tokens map[string]idToken
}

type idToken struct {
	value string
	until time.Time
}

func newRunners(noAuth bool) *Runners {
	return &Runners{
		noAuth: noAuth,
		tokens: map[string]idToken{},
		http: &http.Client{Transport: &http.Transport{
			MaxIdleConns:        512,
			MaxIdleConnsPerHost: 256,
			IdleConnTimeout:     90 * time.Second,
			ForceAttemptHTTP2:   true,
		}},
	}
}

// identity fetches a Google-signed ID token for the runner from the metadata
// server. Cloud Run verifies it before the request reaches the container.
func (r *Runners) identity(ctx context.Context, audience string) (string, error) {
	r.mu.Lock()
	t, ok := r.tokens[audience]
	r.mu.Unlock()
	if ok && time.Now().Before(t.until) {
		return t.value, nil
	}
	u := "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity?audience=" + url.QueryEscape(audience)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("metadata identity: HTTP %d", resp.StatusCode)
	}
	r.mu.Lock()
	r.tokens[audience] = idToken{value: string(body), until: time.Now().Add(45 * time.Minute)}
	r.mu.Unlock()
	return string(body), nil
}

func (r *Runners) once(ctx context.Context, base string, payload []byte) (*proto.RunResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/run", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if !r.noAuth {
		tok, err := r.identity(ctx, base)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errBusy, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	switch {
	case resp.StatusCode == 200:
		// The job started. A cut-off or unparseable body means the instance
		// went away while running it.
		var out proto.RunResponse
		if readErr != nil || json.Unmarshal(body, &out) != nil {
			return nil, errCrashed
		}
		return &out, nil
	case resp.StatusCode == 429 || resp.StatusCode >= 500:
		// Answered by Cloud Run, not by a runner: nothing ran.
		return nil, fmt.Errorf("%w: HTTP %d", errBusy, resp.StatusCode)
	default:
		return nil, fmt.Errorf("runner HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body[:min(len(body), 300)]))
	}
}

// Run sends one program with its tests to a runner, retrying briefly when
// the runner has no free instance.
func (r *Runners) Run(ctx context.Context, base string, in *proto.RunRequest, patience time.Duration) (*proto.RunResponse, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(patience)
	wait := 150 * time.Millisecond
	crashes := 0
	for {
		out, err := r.once(ctx, base, payload)
		if errors.Is(err, errCrashed) {
			if crashes++; crashes > 1 {
				return nil, err
			}
			continue // once more: the instance may have died for another reason
		}
		if err == nil || !errors.Is(err, errBusy) {
			return out, err
		}
		if time.Now().Add(wait).After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		if wait < 2*time.Second {
			wait *= 2
		}
	}
}

// execute runs a group of submissions that share one program. It fills in
// their results; it returns errBusy only when nothing was attempted and a
// later retry makes sense.
func (a *App) execute(ctx context.Context, subs []*Submission, patience time.Duration) error {
	first := subs[0]
	lang := a.langs.get(first.LanguageID)
	now := time.Now()
	if lang == nil {
		for _, s := range subs {
			s.fail(StInternal, "language is no longer available", now)
		}
		return nil
	}
	req := &proto.RunRequest{
		Language:        lang.Lang,
		Source:          first.Source,
		CompilerOptions: first.CompilerOpts,
		Args:            first.Args,
		MergeStderr:     first.RedirectStderr,
		Limits: proto.Limits{
			CPUMs: first.CPUMs, WallMs: first.WallMs, MemoryKB: first.MemoryKB,
			OutputKB: first.FileKB, Procs: first.Procs,
		},
	}
	if first.Network {
		c := a.cfg
		req.Network = &proto.Network{
			WallMs:     first.NetMs,
			RxKB:       c.MaxDownloadKB + c.MaxDownloadKB*netOverheadPct/100 + 1024,
			FileKB:     c.MaxDownloadKB,
			CPUUsPerMB: c.NetCPUUsPerMB,
		}
	}
	if len(first.Files) > 0 {
		req.Files, _ = unzip(first.Files)
	}
	for _, s := range subs {
		req.Tests = append(req.Tests, proto.Test{Stdin: s.Stdin})
	}

	t0 := time.Now()
	resp, err := a.runners.Run(ctx, lang.URL, req, patience)
	rpcMs := int(time.Since(t0) / time.Millisecond)
	now = time.Now()
	if errors.Is(err, errBusy) {
		return err
	}
	if err != nil {
		for _, s := range subs {
			s.fail(StInternal, err.Error(), now)
		}
		return nil
	}

	switch {
	case resp.Error != "":
		for _, s := range subs {
			s.fail(StInternal, resp.Error, now)
		}
	case resp.Compile != nil && !resp.Compile.OK:
		for _, s := range subs {
			s.CompileOutput = resp.Compile.Output
			s.fail(StCompileError, "", now)
		}
	default:
		for i, s := range subs {
			if resp.Compile != nil {
				s.CompileOutput = resp.Compile.Output
			}
			if i >= len(resp.Tests) {
				s.fail(StInternal, "runner returned too few results", now)
				continue
			}
			s.apply(&resp.Tests[i], now)
		}
	}
	m := resp.Meta
	for _, s := range subs {
		s.timing = runTiming{rpcMs: rpcMs, runnerMs: m.TotalMs, boxMs: m.BoxMs, waitMs: m.SandboxWaitMs, warm: m.Warm}
	}
	if first.Network {
		rx, credit := 0, 0
		if len(resp.Tests) > 0 {
			rx, credit = resp.Tests[0].NetRxKB, resp.Tests[0].NetCreditMs
		}
		log.Printf(`{"message":"run","language":%q,"tests":%d,"status":%d,"rpc_ms":%d,"runner_ms":%d,"box_ms":%d,"sandbox_wait_ms":%d,"warm":%t,"instance":%q,"network":true,"net_rx_kb":%d,"net_credit_ms":%d,"host_rx_kb":%d}`,
			lang.Lang, len(subs), first.StatusID, rpcMs, m.TotalMs, m.BoxMs, m.SandboxWaitMs, m.Warm, m.Instance, rx, credit, m.HostRxKB)
		return nil
	}
	log.Printf(`{"message":"run","language":%q,"tests":%d,"status":%d,"rpc_ms":%d,"runner_ms":%d,"box_ms":%d,"sandbox_wait_ms":%d,"warm":%t,"instance":%q}`,
		lang.Lang, len(subs), first.StatusID, rpcMs, m.TotalMs, m.BoxMs, m.SandboxWaitMs, m.Warm, m.Instance)
	return nil
}

// checkCallbackURL rejects callback targets inside the platform's own
// network, so a caller cannot make the API probe internal addresses.
func checkCallbackURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("must be an http or https URL")
	}
	ips, err := net.LookupIP(u.Hostname())
	if err != nil || len(ips) == 0 {
		return errors.New("host does not resolve")
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return errors.New("must not point at a private or local address")
		}
	}
	return nil
}

var callbackClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // a redirect could lead back inside
	},
}

// callback PUTs the finished submission to the caller's URL, as Judge0 does.
func (a *App) callback(ctx context.Context, s *Submission) {
	if s.CallbackURL == "" || checkCallbackURL(s.CallbackURL) != nil {
		return
	}
	body, _ := json.Marshal(a.render(s, defaultFields, true))
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.CallbackURL, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := callbackClient.Do(req)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return
			}
		}
		time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
	}
}
