package main

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
)

// Analytics that outlive the submissions they describe.
//
// Submissions are deleted after a few days. What the dashboard needs is kept
// separately as counters, one document per hour and one per day, per
// language: how many finished, with which verdicts, a latency histogram,
// and sums for run time and memory. Nothing in them identifies a submission,
// so they are kept indefinitely.
//
// Writing a counter document on every submission would not scale: one
// document takes only a few writes per second. Each instance therefore adds
// up what it has seen in memory and writes the totals every few seconds as
// atomic increments, on the back of a request it is already handling (Cloud
// Run gives no CPU between requests) and once more when it shuts down.

// latBoundsMs are the upper bounds of the latency histogram buckets, in
// milliseconds from created_at to finished_at. One more bucket catches the
// rest. Percentiles on the dashboard are read off this, so they are
// approximate to within a bucket.
var latBoundsMs = []int{25, 50, 100, 200, 400, 800, 1500, 3000, 6000, 12000, 30000, 60000, 120000, 300000}

const statsFlushEvery = 10 * time.Second

func latBucket(ms int) int {
	for i, b := range latBoundsMs {
		if ms <= b {
			return i
		}
	}
	return len(latBoundsMs)
}

// tally is what one instance has counted for one language in one hour.
type tally struct {
	n, ran, sync, warm  int64
	latMs, cpuMs, memKB int64
	status              map[int]int64
	hist                []int64
}

type statsBuf struct {
	mu      sync.Mutex
	hours   map[string]map[string]*tally // "2026111414" -> language -> tally
	tokens  map[string]int64             // temporary token id -> submissions accepted
	flushed time.Time
}

func newStatsBuf() *statsBuf {
	return &statsBuf{hours: map[string]map[string]*tally{}, tokens: map[string]int64{}, flushed: time.Now()}
}

// record counts one finished submission.
func (b *statsBuf) record(lang string, s *Submission, sync bool) {
	if s.FinishedAt.IsZero() {
		return
	}
	hour := s.FinishedAt.UTC().Format("2006010215")
	b.mu.Lock()
	defer b.mu.Unlock()
	langs := b.hours[hour]
	if langs == nil {
		langs = map[string]*tally{}
		b.hours[hour] = langs
	}
	t := langs[lang]
	if t == nil {
		t = &tally{status: map[int]int64{}, hist: make([]int64, len(latBoundsMs)+1)}
		langs[lang] = t
	}
	lat := int(s.FinishedAt.Sub(s.CreatedAt) / time.Millisecond)
	if lat < 0 {
		lat = 0
	}
	t.n++
	t.status[s.StatusID]++
	t.hist[latBucket(lat)]++
	t.latMs += int64(lat)
	if s.Ran {
		t.ran++
		t.cpuMs += int64(s.TimeMs)
		t.memKB += int64(s.UsedKB)
	}
	if sync {
		t.sync++
	}
	if s.timing.warm {
		t.warm++
	}
}

// charge counts submissions accepted from a temporary token.
func (b *statsBuf) charge(tokenID string, n int) {
	if tokenID == "" || n <= 0 {
		return
	}
	b.mu.Lock()
	b.tokens[tokenID] += int64(n)
	b.mu.Unlock()
}

func inc(n int64) any { return firestore.Increment(n) }

// counters turns a tally into increments on a stats document's language map.
func (t *tally) counters() map[string]any {
	status := map[string]any{}
	for id, n := range t.status {
		status[strconv.Itoa(id)] = inc(n)
	}
	hist := map[string]any{}
	for i, n := range t.hist {
		if n > 0 {
			hist[strconv.Itoa(i)] = inc(n)
		}
	}
	return map[string]any{
		"n": inc(t.n), "ran": inc(t.ran), "sync": inc(t.sync), "warm": inc(t.warm),
		"lat_ms": inc(t.latMs), "cpu_ms": inc(t.cpuMs), "mem_kb": inc(t.memKB),
		"s": status, "h": hist,
	}
}

// flush writes what has been counted. With force false it does nothing until
// statsFlushEvery has passed since the last write.
func (b *statsBuf) flush(ctx context.Context, st *Store, force bool) {
	b.mu.Lock()
	if (!force && time.Since(b.flushed) < statsFlushEvery) || (len(b.hours) == 0 && len(b.tokens) == 0) {
		b.mu.Unlock()
		return
	}
	hours, tokens := b.hours, b.tokens
	b.hours, b.tokens, b.flushed = map[string]map[string]*tally{}, map[string]int64{}, time.Now()
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for hour, langs := range hours {
		start, err := time.Parse("2006010215", hour)
		if err != nil {
			continue
		}
		l := map[string]any{}
		for lang, t := range langs {
			l[lang] = t.counters()
		}
		// The same increments go to the hour and to its day, so a long range
		// can be read from 90 documents instead of 2,160.
		for id, at := range map[string]time.Time{"h-" + hour: start, "d-" + hour[:8]: start.Truncate(24 * time.Hour)} {
			if _, err := st.c.Collection("stats").Doc(id).Set(ctx, map[string]any{"t": at, "l": l}, firestore.MergeAll); err != nil {
				log.Printf(`{"severity":"WARNING","message":"stats write: %v"}`, err)
			}
		}
	}
	for id, n := range tokens {
		// A revoked token's document is gone; its last few counts go with it.
		st.c.Collection("tokens").Doc(id).Update(ctx, []firestore.Update{{Path: "used", Value: inc(n)}})
	}
}

var statsRanges = map[string]struct {
	prefix string
	step   time.Duration
	count  int
	layout string
}{
	"24h": {"h-", time.Hour, 24, "2006010215"},
	"7d":  {"h-", time.Hour, 168, "2006010215"},
	"30d": {"d-", 24 * time.Hour, 30, "20060102"},
	"90d": {"d-", 24 * time.Hour, 90, "20060102"},
}

// handleStats returns the counter documents for a range, oldest first, with
// empty buckets included so the dashboard's time axis has no gaps.
func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	rng, ok := statsRanges[r.URL.Query().Get("range")]
	if !ok {
		rng = statsRanges["24h"]
	}
	// Show this instance's own latest counts too.
	a.stats.flush(r.Context(), a.store, true)

	end := time.Now().UTC().Truncate(rng.step)
	refs := make([]*firestore.DocumentRef, rng.count)
	times := make([]time.Time, rng.count)
	for i := range refs {
		times[i] = end.Add(-time.Duration(rng.count-1-i) * rng.step)
		refs[i] = a.store.c.Collection("stats").Doc(rng.prefix + times[i].Format(rng.layout))
	}
	snaps, err := a.store.c.GetAll(r.Context(), refs)
	if err != nil {
		log.Printf(`{"severity":"ERROR","message":"stats read: %v"}`, err)
		errorJSON(w, 503, "could not read analytics")
		return
	}
	buckets := make([]map[string]any, rng.count)
	for i, snap := range snaps {
		buckets[i] = map[string]any{"t": times[i].Format(time.RFC3339), "l": map[string]any{}}
		if snap.Exists() {
			if l, ok := snap.Data()["l"]; ok {
				buckets[i]["l"] = l
			}
		}
	}
	writeJSON(w, 200, map[string]any{
		"step_seconds":      int(rng.step / time.Second),
		"latency_bounds_ms": latBoundsMs,
		"statuses":          statusNames,
		"buckets":           buckets,
	})
}
