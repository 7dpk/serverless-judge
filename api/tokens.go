package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Two kinds of key are accepted in X-Auth-Token.
//
// An admin key (from the AUTH_TOKENS secret) can do everything, including
// minting temporary tokens and reading analytics.
//
// A temporary token is what an admin hands to someone else to try the judge.
// It can submit and read submissions, nothing more. It expires (7 days at
// most) and carries a cap on how many submissions it may make. Only its
// SHA-256 is stored, so the token itself cannot be recovered from the
// database, and Firestore deletes the record once it has expired.

const (
	tempPrefix       = "jt_"
	maxTokenTTL      = 7 * 24 * time.Hour
	defaultTokenRuns = 500
	maxTokenRuns     = 100000
	tokenCacheFor    = 30 * time.Second
)

type TempToken struct {
	Label     string    `firestore:"label" json:"label"`
	CreatedAt time.Time `firestore:"created_at" json:"created_at"`
	ExpiresAt time.Time `firestore:"expires_at" json:"expires_at"`
	Max       int64     `firestore:"max_submissions" json:"max_submissions"`
	Used      int64     `firestore:"used" json:"used"`
}

func tokenID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// cachedToken is one instance's view of a temporary token. The submission
// count is exact for what this instance has accepted and up to
// tokenCacheFor behind for what others have, so the cap can be overshot a
// little when several instances serve the same token at once.
type cachedToken struct {
	mu      sync.Mutex
	tok     TempToken
	exists  bool
	fetched time.Time
	local   int64 // accepted here since tok was fetched
}

type principal struct {
	admin   bool
	tokenID string       // set for temporary tokens
	entry   *cachedToken // set for temporary tokens
}

type ctxKey struct{}

func who(r *http.Request) principal {
	p, _ := r.Context().Value(ctxKey{}).(principal)
	return p
}

// lookup returns the cached record for a temporary token, refreshing it from
// Firestore when stale. Unknown tokens are cached too, so guessing does not
// turn into a read per guess.
func (a *App) lookup(ctx context.Context, id string) *cachedToken {
	v, _ := a.tokens.LoadOrStore(id, &cachedToken{})
	c := v.(*cachedToken)
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.fetched) < tokenCacheFor {
		return c
	}
	snap, err := a.store.c.Collection("tokens").Doc(id).Get(ctx)
	switch {
	case status.Code(err) == codes.NotFound:
		c.exists = false
	case err != nil:
		return c // keep what we had; try again on the next request
	default:
		var t TempToken
		if snap.DataTo(&t) == nil {
			c.tok, c.exists = t, true
		}
	}
	c.fetched, c.local = time.Now(), 0
	return c
}

// authenticate identifies the caller. The second result is an error message
// for the caller when the key is recognised but no longer usable.
func (a *App) authenticate(r *http.Request) (principal, string, bool) {
	got := r.Header.Get("X-Auth-Token")
	if got == "" {
		return principal{}, "", false
	}
	for _, t := range a.cfg.AuthTokens {
		if same(got, t) {
			return principal{admin: true}, "", true
		}
	}
	if len(got) < len(tempPrefix)+16 || got[:len(tempPrefix)] != tempPrefix {
		return principal{}, "", false
	}
	id := tokenID(got)
	c := a.lookup(r.Context(), id)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case !c.exists:
		return principal{}, "", false
	case time.Now().After(c.tok.ExpiresAt):
		return principal{}, "this token has expired", false
	}
	return principal{tokenID: id, entry: c}, "", true
}

// spend takes n submissions from a temporary token's allowance. Admin keys
// have no allowance to spend.
func (a *App) spend(p principal, n int) bool {
	if p.admin || p.entry == nil || n <= 0 {
		return true
	}
	c := p.entry
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok.Max > 0 && c.tok.Used+c.local+int64(n) > c.tok.Max {
		return false
	}
	c.local += int64(n)
	a.stats.charge(p.tokenID, n)
	return true
}

// auth admits any valid key. admin admits admin keys only.
func (a *App) auth(next http.HandlerFunc) http.HandlerFunc  { return a.guard(next, false) }
func (a *App) admin(next http.HandlerFunc) http.HandlerFunc { return a.guard(next, true) }

func (a *App) guard(next http.HandlerFunc, adminOnly bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, why, ok := a.authenticate(r)
		switch {
		case !ok && why != "":
			errorJSON(w, http.StatusUnauthorized, why)
		case !ok:
			w.WriteHeader(http.StatusUnauthorized)
		case adminOnly && !p.admin:
			errorJSON(w, http.StatusForbidden, "this needs an admin key")
		default:
			next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
		}
	}
}

// handleWhoAmI lets the UI tell an admin key from a temporary token and show
// what is left on the latter.
func (a *App) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if p.admin {
		writeJSON(w, 200, map[string]any{"role": "admin"})
		return
	}
	c := p.entry
	c.mu.Lock()
	defer c.mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"role": "temporary", "label": c.tok.Label, "expires_at": c.tok.ExpiresAt,
		"max_submissions": c.tok.Max, "used": c.tok.Used + c.local,
	})
}

func (a *App) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label    string  `json:"label"`
		TTLHours float64 `json:"ttl_hours"`
		Max      *int64  `json:"max_submissions"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		errorJSON(w, 400, "request body is not valid JSON")
		return
	}
	ttl := time.Duration(in.TTLHours * float64(time.Hour))
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if ttl > maxTokenTTL {
		errorJSON(w, 422, "ttl_hours must be at most 168 (7 days)")
		return
	}
	max := int64(defaultTokenRuns)
	if in.Max != nil {
		max = *in.Max
	}
	if max < 1 || max > maxTokenRuns {
		errorJSON(w, 422, "max_submissions must be between 1 and 100000")
		return
	}
	if len(in.Label) > 80 {
		errorJSON(w, 422, "label is too long (maximum is 80 characters)")
		return
	}
	raw := make([]byte, 20)
	rand.Read(raw)
	token := tempPrefix + hex.EncodeToString(raw)
	now := time.Now().UTC()
	t := TempToken{Label: in.Label, CreatedAt: now, ExpiresAt: now.Add(ttl), Max: max}
	if _, err := a.store.c.Collection("tokens").Doc(tokenID(token)).Set(r.Context(), t); err != nil {
		log.Printf(`{"severity":"ERROR","message":"token create: %v"}`, err)
		errorJSON(w, 503, "could not store the token")
		return
	}
	// The token is shown once, here. Only its hash is kept.
	writeJSON(w, 201, map[string]any{
		"token": token, "id": tokenID(token), "label": t.Label,
		"expires_at": t.ExpiresAt, "max_submissions": t.Max,
	})
}

func (a *App) handleTokenList(w http.ResponseWriter, r *http.Request) {
	a.stats.flush(r.Context(), a.store, true) // so "used" is current for this instance
	docs, err := a.store.c.Collection("tokens").Documents(r.Context()).GetAll()
	if err != nil {
		log.Printf(`{"severity":"ERROR","message":"token list: %v"}`, err)
		errorJSON(w, 503, "could not read tokens")
		return
	}
	type row struct {
		ID string `json:"id"`
		TempToken
		Expired bool `json:"expired"`
	}
	out := []row{}
	now := time.Now()
	for _, d := range docs {
		var t TempToken
		if d.DataTo(&t) == nil {
			out = append(out, row{ID: d.Ref.ID, TempToken: t, Expired: now.After(t.ExpiresAt)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	writeJSON(w, 200, out)
}

// handleTokenRevoke deletes a temporary token. Other instances may go on
// accepting it for up to tokenCacheFor.
func (a *App) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if len(id) != 64 {
		errorJSON(w, 404, "no such token")
		return
	}
	if _, err := a.store.c.Collection("tokens").Doc(id).Delete(r.Context()); err != nil {
		log.Printf(`{"severity":"ERROR","message":"token revoke: %v"}`, err)
		errorJSON(w, 503, "could not revoke the token")
		return
	}
	a.tokens.Delete(id)
	w.WriteHeader(http.StatusNoContent)
}
