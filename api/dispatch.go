package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"cloud.google.com/go/pubsub/v2"
)

// How queued (wait=false) submissions get run.
//
// The API publishes one Pub/Sub message per group of submissions and returns
// the tokens. A push subscription delivers each message as an HTTP request
// to the worker service (this same binary with ROLE=worker), and that
// request does the work. Pub/Sub keeps the message until the worker answers
// 2xx, redelivers it otherwise, and widens or narrows how many deliveries it
// has in flight according to how they are going.
//
// Where work waits when there is more of it than runners:
//
// Each worker instance has one gate per language family, as wide as that
// family's instance cap. A delivery waits at the gate, in memory and in
// arrival order, until a place is free, then runs. Anything Pub/Sub has not
// delivered yet waits in Pub/Sub. Either way nothing polls a busy runner.
//
// Why Pub/Sub. Each of these was built and measured at 167 submissions/s:
//
//   - Cloud Tasks. A cold queue sends 1 task/s for its first minute, needs
//     minutes to reach 75/s, and slows again whenever its target answers 429
//     or 503. Runners sat idle while thousands of submissions waited.
//   - The API calling itself and returning. The request doing the work is
//     held open by the instance that sent it. When that instance goes idle
//     Cloud Run stops giving it CPU, its connections drop, and the work it
//     was holding open is orphaned.
//
// Pub/Sub owns the delivery, so no instance of ours has to stay awake for
// it.
const (
	gateWait     = 4 * time.Minute  // how long a delivery waits at the gate
	runPatience  = 2 * time.Minute  // then, at the front, how long it keeps trying for a runner
	maxQueueAge  = 20 * time.Minute // older than this and the submission is given up on
	defaultGate  = 8
	publishLimit = 15 * time.Second
)

type group struct {
	Tokens     []string `json:"tokens"`
	LanguageID int      `json:"language_id"`
}

// gate returns the channel that limits how many groups of one language
// family this instance runs at a time. Languages served by the same runner
// share it.
func (a *App) gate(l *Language) chan struct{} {
	n := l.MaxInstances
	if n <= 0 {
		n = defaultGate
	}
	key := l.URL + "#" + strconv.Itoa(n)
	if g, ok := a.gates.Load(key); ok {
		return g.(chan struct{})
	}
	g, _ := a.gates.LoadOrStore(key, make(chan struct{}, n))
	return g.(chan struct{})
}

// enter waits up to wait for a place at the language's gate. On success it
// returns the function that gives the place back.
func (a *App) enter(ctx context.Context, l *Language, wait time.Duration) (leave func(), ok bool) {
	if l == nil { // unknown language: nothing to queue behind
		return func() {}, true
	}
	g := a.gate(l)
	leave = func() { <-g }
	select {
	case g <- struct{}{}:
		return leave, true
	default:
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case g <- struct{}{}:
		return leave, true
	case <-t.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

// dispatch queues stored submissions, one message per group. It returns the
// groups that could not be queued.
func (a *App) dispatch(ctx context.Context, groups []group) (lost []group) {
	// Queuing must not be undone by the caller hanging up.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishLimit)
	defer cancel()
	results := make([]*pubsub.PublishResult, len(groups))
	for i, g := range groups {
		body, _ := json.Marshal(g)
		results[i] = a.queue.Publish(ctx, &pubsub.Message{Data: body})
	}
	for i, res := range results {
		if _, err := res.Get(ctx); err != nil {
			log.Printf(`{"severity":"ERROR","message":"publish: %v"}`, err)
			lost = append(lost, groups[i])
		}
	}
	return lost
}

// pushEnvelope is the body Pub/Sub sends to a push endpoint.
type pushEnvelope struct {
	Message struct {
		Data        []byte    `json:"data"`
		MessageID   string    `json:"messageId"`
		PublishTime time.Time `json:"publishTime"`
	} `json:"message"`
}

// unfinished returns the submissions that still need running. Pub/Sub may
// deliver a message more than once.
func unfinished(all []*Submission) []*Submission {
	var todo []*Submission
	for _, s := range all {
		if s != nil && !s.done() {
			todo = append(todo, s)
		}
	}
	return todo
}

// handlePush runs one queued group. Only the worker service serves it, and
// Cloud Run lets only the push subscription's identity call that service.
//
// 2xx tells Pub/Sub the message is dealt with. Anything else makes it
// deliver again later, and deliver a little less eagerly.
func (a *App) handlePush(w http.ResponseWriter, r *http.Request) {
	a.handlePushWith(w, r, gateWait)
}

func (a *App) handlePushWith(w http.ResponseWriter, r *http.Request, wait time.Duration) {
	var env pushEnvelope
	var g group
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&env); err != nil ||
		json.Unmarshal(env.Message.Data, &g) != nil || len(g.Tokens) == 0 {
		w.WriteHeader(http.StatusNoContent) // malformed: redelivery cannot help
		return
	}
	// Finish the work even if the delivery is abandoned on Pub/Sub's side.
	ctx := context.WithoutCancel(r.Context())
	expired := !env.Message.PublishTime.IsZero() && time.Since(env.Message.PublishTime) > maxQueueAge

	// Wait for a place. Nothing is loaded until then, so a waiting group
	// costs a few bytes.
	if !expired {
		leave, ok := a.enter(ctx, a.langs.get(g.LanguageID), wait)
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer leave()
	}

	all, err := a.store.GetAll(ctx, g.Tokens)
	if err != nil {
		log.Printf(`{"severity":"ERROR","message":"queue load: %v"}`, err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	todo := unfinished(all)
	if len(todo) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if expired {
		for _, s := range todo {
			s.fail(StInternal, "no runner capacity: gave up after "+maxQueueAge.String()+" in the queue", time.Now())
		}
	} else if err := a.execute(ctx, todo, runPatience); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable) // runners stayed busy: try again later
		return
	}

	saveCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := a.store.PutAll(saveCtx, todo); err != nil {
		log.Printf(`{"severity":"ERROR","message":"queue save: %v"}`, err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var wg sync.WaitGroup
	for _, s := range todo {
		if s.CallbackURL != "" {
			wg.Add(1)
			go func(s *Submission) { defer wg.Done(); a.callback(saveCtx, s) }(s)
		}
	}
	wg.Wait()
	a.record(todo, false)
	w.WriteHeader(http.StatusNoContent)
	a.stats.flush(saveCtx, a.store, false)
}
