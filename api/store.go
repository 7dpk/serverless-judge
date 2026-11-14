package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Firestore rejects documents over 1 MiB. Blobs are gzip-packed, and if a
// document is still too big its outputs are cut to fit.
const (
	docBudget    = 900 << 10
	outputKeepKB = 64
)

var errNotFound = errors.New("not found")

func newToken() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func gz(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func gunzip(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	out, _ := io.ReadAll(r)
	return out
}

type Store struct {
	c *firestore.Client
}

func (st *Store) subs() *firestore.CollectionRef { return st.c.Collection("submissions") }

// pack returns the document to write: a copy with gzip-packed blobs.
func pack(s *Submission) *Submission {
	d := *s
	blobs := func() int {
		d.Source, d.Stdin, d.Expected, d.Files = gz(s.Source), gz(s.Stdin), gz(s.Expected), gz(s.Files)
		d.Stdout, d.Stderr, d.CompileOutput = gz(s.Stdout), gz(s.Stderr), gz(s.CompileOutput)
		return len(d.Source) + len(d.Stdin) + len(d.Expected) + len(d.Files) +
			len(d.Stdout) + len(d.Stderr) + len(d.CompileOutput)
	}
	if blobs() > docBudget {
		cut := func(b []byte) []byte {
			if len(b) > outputKeepKB<<10 {
				return b[:outputKeepKB<<10]
			}
			return b
		}
		t := *s
		t.Stdout, t.Stderr, t.CompileOutput = cut(s.Stdout), cut(s.Stderr), cut(s.CompileOutput)
		s = &t
		d = t
		if d.Message != "" {
			d.Message += "; "
		}
		d.Message += "stored output was truncated"
		if blobs() > docBudget {
			// Inputs alone are too large to keep. Keep the verdict.
			d.Stdin, d.Expected, d.Files = nil, nil, nil
		}
	}
	return &d
}

func unpack(d *Submission) *Submission {
	d.Source, d.Stdin, d.Expected, d.Files = gunzip(d.Source), gunzip(d.Stdin), gunzip(d.Expected), gunzip(d.Files)
	d.Stdout, d.Stderr, d.CompileOutput = gunzip(d.Stdout), gunzip(d.Stderr), gunzip(d.CompileOutput)
	return d
}

func (st *Store) Put(ctx context.Context, s *Submission) error {
	_, err := st.subs().Doc(s.Token).Set(ctx, pack(s))
	return err
}

func (st *Store) PutAll(ctx context.Context, subs []*Submission) error {
	if len(subs) == 1 {
		return st.Put(ctx, subs[0])
	}
	bw := st.c.BulkWriter(ctx)
	jobs := make([]*firestore.BulkWriterJob, 0, len(subs))
	for _, s := range subs {
		j, err := bw.Set(st.subs().Doc(s.Token), pack(s))
		if err != nil {
			bw.End()
			return err
		}
		jobs = append(jobs, j)
	}
	bw.End()
	for _, j := range jobs {
		if _, err := j.Results(); err != nil {
			return err
		}
	}
	return nil
}

func (st *Store) Get(ctx context.Context, token string) (*Submission, error) {
	snap, err := st.subs().Doc(token).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	var d Submission
	if err := snap.DataTo(&d); err != nil {
		return nil, err
	}
	return unpack(&d), nil
}

// GetAll returns submissions in the order asked; missing ones are nil.
func (st *Store) GetAll(ctx context.Context, tokens []string) ([]*Submission, error) {
	refs := make([]*firestore.DocumentRef, len(tokens))
	for i, t := range tokens {
		refs[i] = st.subs().Doc(t)
	}
	snaps, err := st.c.GetAll(ctx, refs)
	if err != nil {
		return nil, err
	}
	out := make([]*Submission, len(tokens))
	for i, snap := range snaps {
		if !snap.Exists() {
			continue
		}
		var d Submission
		if err := snap.DataTo(&d); err != nil {
			return nil, err
		}
		out[i] = unpack(&d)
	}
	return out, nil
}

// Language is one row of the registry: a Judge0-style id mapped to a runner
// service and the manifest name inside it. deploy.sh writes these.
type Language struct {
	ID         int    `firestore:"id" json:"id"`
	Name       string `firestore:"name" json:"name"`
	Lang       string `firestore:"lang" json:"key"` // not in Judge0: lets a client group ids of one language
	URL        string `firestore:"url" json:"-"`
	SourceFile string `firestore:"source_file" json:"-"`
	// MaxInstances is the runner service's instance cap: how many programs
	// of this family can run at once.
	MaxInstances int `firestore:"max_instances" json:"-"`
}

// langCache serves the registry from memory and refreshes it in the
// background, so adding a language needs no API redeploy.
type langCache struct {
	st      *Store
	fetch   func(context.Context) ([]Language, error) // nil: read Firestore
	mu      sync.RWMutex
	byID    map[int]*Language
	list    []*Language
	loaded  time.Time
	loading bool

	missMu    sync.Mutex
	rechecked time.Time // last reload forced by an unknown id
}

const (
	langTTL = 30 * time.Second
	// An unknown id forces a reload, but not more often than this, so
	// nonsense ids cannot be turned into a stream of registry reads.
	langRecheck = 2 * time.Second
)

func (lc *langCache) rows(ctx context.Context) ([]Language, error) {
	if lc.fetch != nil {
		return lc.fetch(ctx)
	}
	docs, err := lc.st.c.Collection("languages").Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	rows := make([]Language, 0, len(docs))
	for _, d := range docs {
		var l Language
		if d.DataTo(&l) == nil {
			rows = append(rows, l)
		}
	}
	return rows, nil
}

func (lc *langCache) load(ctx context.Context) error {
	rows, err := lc.rows(ctx)
	if err != nil {
		return err
	}
	byID := map[int]*Language{}
	var list []*Language
	for i := range rows {
		l := &rows[i]
		if l.ID == 0 || l.URL == "" {
			continue
		}
		byID[l.ID] = l
		list = append(list, l)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	lc.mu.Lock()
	lc.byID, lc.list, lc.loaded = byID, list, time.Now()
	lc.mu.Unlock()
	return nil
}

func (lc *langCache) refreshIfStale() {
	lc.mu.Lock()
	stale := time.Since(lc.loaded) > langTTL && !lc.loading
	if stale {
		lc.loading = true
	}
	lc.mu.Unlock()
	if !stale {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		lc.load(ctx)
		lc.mu.Lock()
		lc.loading = false
		lc.mu.Unlock()
	}()
}

// get returns the language with this id, or nil. The background refresh
// only runs while there is traffic: an instance that sat idle still holds
// the registry as it was, and a language may have been registered a second
// ago. So a miss is checked against the registry before it is believed.
func (lc *langCache) get(id int) *Language {
	lc.refreshIfStale()
	if l := lc.cached(id); l != nil {
		return l
	}
	lc.recheck()
	return lc.cached(id)
}

func (lc *langCache) cached(id int) *Language {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.byID[id]
}

// recheck reloads the registry now, unless it was loaded or rechecked
// within langRecheck. Concurrent misses wait for one reload.
func (lc *langCache) recheck() {
	if lc.fetch == nil && lc.st == nil {
		return
	}
	lc.missMu.Lock()
	defer lc.missMu.Unlock()
	lc.mu.RLock()
	fresh := time.Since(lc.loaded) < langRecheck
	lc.mu.RUnlock()
	if fresh || time.Since(lc.rechecked) < langRecheck {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lc.load(ctx)
	lc.rechecked = time.Now()
}

func (lc *langCache) all() []*Language {
	lc.refreshIfStale()
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.list
}
