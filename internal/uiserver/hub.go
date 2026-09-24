package uiserver

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"antcolony/internal/config"
	"antcolony/internal/simcore"
)

// streamObserver forwards snapshots to a browser.
//
// The send is NON-BLOCKING and drops on a full buffer, which is the single
// most important line in the UI. If the observer blocked while a slow browser
// caught up, the simulation would stall inside its own tick loop and every
// timing the UI reports would be a measurement of the network instead of the
// engine.
type streamObserver struct {
	ch      chan *simcore.Snapshot
	every   int
	dropped atomic.Int64
}

func (o *streamObserver) Interval() int { return o.every }

func (o *streamObserver) OnTick(_ int, s *simcore.Snapshot) {
	select {
	case o.ch <- s:
	default:
		o.dropped.Add(1)
	}
}

// run is one live simulation.
type run struct {
	id     string
	cfg    config.Config
	engine string
	obs    *streamObserver
	cancel context.CancelFunc

	mu   sync.Mutex
	done bool
	res  simcore.Result
	err  error
}

// hub owns every live run.
type hub struct {
	mu   sync.Mutex
	runs map[string]*run
	next atomic.Int64
	// unlimited drops runTimeout (Server.DisableLimits).
	unlimited bool
}

func newHub() *hub { return &hub{runs: make(map[string]*run)} }

// start launches a simulation in the background and returns its handle.
func (h *hub) start(cfg config.Config, engineName string, interval int) (*run, error) {
	eng, err := simcore.New(engineName)
	if err != nil {
		return nil, err
	}
	if interval < 1 {
		interval = 1
	}

	// A live run stops at runTimeout even if nobody presses Stop: the only
	// thing keeping it alive otherwise is its tick count.
	ctx, cancel := context.WithCancel(context.Background())
	if !h.unlimited {
		ctx, cancel = context.WithTimeout(context.Background(), runTimeout)
	}
	r := &run{
		id:     fmt.Sprintf("r%d", h.next.Add(1)),
		cfg:    cfg,
		engine: engineName,
		// A deep buffer so a browser that connects a moment late still sees
		// the opening frames. Beyond this, frames are dropped, never queued.
		obs:    &streamObserver{ch: make(chan *simcore.Snapshot, 256), every: interval},
		cancel: cancel,
	}

	h.mu.Lock()
	// Only one live run at a time: the UI shows one colony, and concurrent
	// runs would compete for the same cores and make the displayed tick rate
	// meaningless.
	for id, old := range h.runs {
		old.cancel()
		delete(h.runs, id)
	}
	h.runs[r.id] = r
	h.mu.Unlock()

	go func() {
		defer cancel()
		res, err := eng.Run(ctx, cfg, r.obs)
		r.mu.Lock()
		r.res, r.err, r.done = res, err, true
		r.mu.Unlock()
		close(r.obs.ch)
	}()

	return r, nil
}

func (h *hub) get(id string) (*run, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.runs[id]
	return r, ok
}

func (h *hub) stop(id string) bool {
	h.mu.Lock()
	r, ok := h.runs[id]
	h.mu.Unlock()
	if ok {
		r.cancel()
	}
	return ok
}

func (r *run) snapshotResult() (simcore.Result, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.res, r.done, r.err
}
