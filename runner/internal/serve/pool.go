package serve

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"serverless-judge/runner/proto"
)

const (
	maxReportBytes = 96 << 20
	readyTimeout   = 30 * time.Second
)

// sbx is one sandbox with the supervisor already started inside it, blocked
// reading a job from stdin. Each sandbox serves exactly one job.
type sbx struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	ready   chan struct{} // closed when the supervisor says READY
	lines   chan []byte   // report candidates
	exited  chan struct{} // closed when the sandbox process is gone
	stderr  *capBuf
	created time.Time
}

// Pool keeps one spare sandbox so a job does not wait for sandbox creation.
// The spare is created while the previous job runs: Cloud Run only gives the
// container CPU during a request.
type Pool struct {
	sandboxBin string // empty: run the supervisor directly (local development)
	selfBin    string
	langDir    string
	writable   bool
	env        []string // the image's environment, as KEY=VALUE

	mu    sync.Mutex
	spare *sbx

	started atomic.Bool // the instance's first sandbox has been created
}

func NewPool(sandboxBin, selfBin, langDir string, writable bool, env map[string]string) *Pool {
	p := &Pool{sandboxBin: sandboxBin, selfBin: selfBin, langDir: langDir, writable: writable}
	for k, v := range env {
		p.env = append(p.env, k+"="+v)
	}
	sort.Strings(p.env)
	return p
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (p *Pool) spawn() (*sbx, error) {
	// The first sandbox also warms the instance's file cache.
	// (Atomic, not p.mu: Refill calls spawn with the mutex held.)
	boxArgs := []string{"box", p.langDir}
	if p.started.CompareAndSwap(false, true) {
		boxArgs = append(boxArgs, "first")
	}
	s := &sbx{
		name:    "j" + randHex(6),
		ready:   make(chan struct{}),
		lines:   make(chan []byte, 4),
		exited:  make(chan struct{}),
		stderr:  &capBuf{max: 8 << 10},
		created: time.Now(),
	}
	// A sandbox starts with an empty environment. The image's own variables
	// (PATH, JAVA_HOME, GOCACHE and so on) are passed in explicitly so the
	// supervisor can find the toolchain, including during warmup, before
	// any job has arrived. None of them are secrets: the runner has none.
	if p.sandboxBin == "" {
		s.cmd = exec.Command(p.selfBin, boxArgs...)
		s.cmd.Env = p.env
	} else {
		args := []string{"do", "--sandbox-name", s.name, "-w", "/"}
		if p.writable {
			args = append(args, "--write")
		}
		for _, kv := range p.env {
			args = append(args, "-e", kv)
		}
		args = append(args, "--", p.selfBin)
		args = append(args, boxArgs...)
		s.cmd = exec.Command(p.sandboxBin, args...)
		s.cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root"}
	}
	s.cmd.Stderr = s.stderr

	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	s.cmd.Stdin, s.cmd.Stdout, s.stdin = inR, outW, inW
	if err := s.cmd.Start(); err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, err
	}
	inR.Close()
	outW.Close()

	go func() {
		defer close(s.lines)
		defer outR.Close()
		r := bufio.NewReaderSize(outR, 1<<16)
		sawReady := false
		for {
			line, err := readLine(r, maxReportBytes)
			if len(line) > 0 {
				if !sawReady && bytes.Equal(bytes.TrimSpace(line), []byte("READY")) {
					sawReady = true
					close(s.ready)
				} else {
					select {
					case s.lines <- line:
					default: // nobody is waiting for more than a few lines
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		s.cmd.Wait()
		close(s.exited)
	}()
	return s, nil
}

// readLine reads up to and including '\n', failing on oversized lines so a
// misbehaving sandbox cannot exhaust the runner's memory.
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > max {
			return nil, errors.New("line too long")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, err
	}
}

// take returns the spare if there is one, otherwise a new sandbox.
func (p *Pool) take() (s *sbx, warm bool, err error) {
	p.mu.Lock()
	s, p.spare = p.spare, nil
	p.mu.Unlock()
	if s != nil {
		select {
		case <-s.exited: // died while idle
			s = nil
		default:
		}
	}
	if s != nil {
		select {
		case <-s.ready:
			warm = true
		default:
		}
		return s, warm, nil
	}
	s, err = p.spawn()
	return s, false, err
}

// Refill makes sure a spare exists.
func (p *Pool) Refill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.spare != nil {
		select {
		case <-p.spare.exited:
			p.spare = nil
		default:
			return
		}
	}
	if s, err := p.spawn(); err == nil {
		p.spare = s
	}
}

func (p *Pool) destroy(s *sbx) {
	s.stdin.Close()
	if p.sandboxBin != "" {
		exec.Command(p.sandboxBin, "delete", "--force", s.name).Run()
	}
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
}

// Close tears down the spare on shutdown.
func (p *Pool) Close() {
	p.mu.Lock()
	s := p.spare
	p.spare = nil
	p.mu.Unlock()
	if s != nil {
		p.destroy(s)
	}
}

// Run executes one job in its own sandbox and returns the supervisor report.
func (p *Pool) Run(job *proto.Job, budget time.Duration) (*proto.RunResponse, error) {
	payload, err := json.Marshal(job)
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		t0 := time.Now()
		s, warm, err := p.take()
		if err != nil {
			lastErr = fmt.Errorf("start sandbox: %w", err)
			continue
		}
		if attempt == 0 {
			go p.Refill()
		}
		select {
		case <-s.ready:
		case <-s.exited:
			lastErr = fmt.Errorf("sandbox exited before it was ready: %s", s.stderr.String())
			continue
		case <-time.After(readyTimeout):
			p.destroy(s)
			lastErr = errors.New("sandbox did not become ready in time")
			continue
		}
		waitMs := int(time.Since(t0) / time.Millisecond)

		go func() {
			s.stdin.Write(payload)
			s.stdin.Close()
		}()

		resp, err := p.await(s, job.Nonce, budget)
		if err != nil {
			return nil, err // the job may have started: never run it twice
		}
		resp.Meta.Warm = warm
		resp.Meta.SandboxWaitMs = waitMs
		return resp, nil
	}
	return nil, lastErr
}

func (p *Pool) await(s *sbx, nonce string, budget time.Duration) (*proto.RunResponse, error) {
	prefix := []byte(nonce + " ")
	deadline := time.NewTimer(budget)
	defer deadline.Stop()
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				return nil, fmt.Errorf("sandbox ended without a report: %s", s.stderr.String())
			}
			if !bytes.HasPrefix(line, prefix) {
				continue // not written by the supervisor
			}
			var resp proto.RunResponse
			if err := json.Unmarshal(line[len(prefix):], &resp); err != nil {
				return nil, fmt.Errorf("bad report: %w", err)
			}
			return &resp, nil
		case <-deadline.C:
			p.destroy(s)
			return nil, errors.New("sandbox exceeded its time budget and was destroyed")
		}
	}
}

// capBuf keeps the first max bytes written to it.
type capBuf struct {
	mu  sync.Mutex
	max int
	b   bytes.Buffer
}

func (c *capBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - c.b.Len(); room > 0 {
		if len(p) > room {
			c.b.Write(p[:room])
		} else {
			c.b.Write(p)
		}
	}
	return len(p), nil
}

func (c *capBuf) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.String()
}
