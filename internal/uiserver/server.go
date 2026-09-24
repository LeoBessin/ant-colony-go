// Package uiserver is the benchmark harness UI: a live view of the colony and
// a side-by-side engine comparison table.
//
// It is strictly a CONSUMER of the simulation. Nothing in this package is
// imported by cmd/antsim, and test/imports_test.go fails the build if that
// ever changes, because HTTP initialisation inside the measured binary would
// corrupt every number in the audit report.
package uiserver

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"runtime"
	"time"

	"antcolony/internal/config"
	"antcolony/internal/simcore"
)

//go:embed static
var staticFS embed.FS

// Server serves the UI.
type Server struct {
	hub     *hub
	mux     *http.ServeMux
	handler http.Handler
	// benchSlot admits one bench at a time. Benches are CPU-bound and the
	// parallel engine uses every core: concurrent ones would only slow each
	// other down and let a handful of requests saturate the host.
	benchSlot chan struct{}
}

// New builds the HTTP handler.
func New() *Server {
	s := &Server{hub: newHub(), mux: http.NewServeMux(), benchSlot: make(chan struct{}, 1)}

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	s.mux.Handle("/", http.FileServer(http.FS(sub)))

	s.mux.HandleFunc("/api/meta", s.handleMeta)
	s.mux.HandleFunc("/api/scenario", s.handleScenario)
	s.mux.HandleFunc("/api/run", s.handleRun)
	s.mux.HandleFunc("/api/stream", s.handleStream)
	s.mux.HandleFunc("/api/stop", s.handleStop)
	s.mux.HandleFunc("/api/result", s.handleResult)
	s.mux.HandleFunc("/api/bench", s.handleBench)
	s.mux.HandleFunc("/healthz", handleHealth)
	s.handler = withGzip(s.mux)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// handleHealth answers the container healthcheck. It touches nothing else so
// a slow bench run can never make the container look dead.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// --- metadata ---------------------------------------------------------------

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"engines":    simcore.Names(),
		"scenarios":  config.ScenarioNames(),
		"go_version": runtime.Version(),
		"gomaxprocs": runtime.GOMAXPROCS(0),
		"num_cpu":    runtime.NumCPU(),
	})
}

func (s *Server) handleScenario(w http.ResponseWriter, r *http.Request) {
	b, err := config.ScenarioBytes(r.URL.Query().Get("name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// --- live run ---------------------------------------------------------------

type runRequest struct {
	Scenario string          `json:"scenario"`
	Config   json.RawMessage `json:"config"` // takes precedence over Scenario
	Engine   string          `json:"engine"`
	Interval int             `json:"interval"`
	Seed     *uint64         `json:"seed"`
	Ticks    *int            `json:"ticks"`
	Ants     *int            `json:"ants"`
}

// resolve turns a request into a validated Config. Overrides are applied
// before validation so a bad override is rejected rather than simulated.
func (req runRequest) resolve() (config.Config, error) {
	var (
		cfg config.Config
		err error
	)
	if len(req.Config) > 0 {
		cfg, err = config.Parse(req.Config)
	} else {
		cfg, err = config.LoadScenario(req.Scenario)
	}
	if err != nil {
		return config.Config{}, err
	}
	if req.Seed != nil {
		cfg.Seed = *req.Seed
	}
	if req.Ticks != nil {
		cfg.Ticks = *req.Ticks
	}
	if req.Ants != nil {
		cfg.AntCount = *req.Ants
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	if err := checkLimits(cfg); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("POST only"))
		return
	}
	var req runRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := req.resolve()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	run, err := s.hub.start(cfg, req.Engine, req.Interval)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":     run.id,
		"engine": req.Engine,
		"width":  cfg.Width,
		"height": cfg.Height,
		"ticks":  cfg.Ticks,
		"ants":   cfg.AntCount,
		"seed":   cfg.Seed,
	})
}

// handleStream pushes snapshots as Server-Sent Events.
//
// SSE rather than WebSocket: the stream is one-way, and SSE needs nothing
// beyond net/http and an http.Flusher. Keeping the whole project on the
// standard library means the dependency list in the audit report is empty.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	run, ok := s.hub.get(r.URL.Query().Get("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no such run"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}

	// The stream outlives the server-wide WriteTimeout by design.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	enc := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case snap, open := <-run.obs.ch:
			if !open {
				res, _, err := run.snapshotResult()
				payload := map[string]any{"result": res, "dropped": run.obs.dropped.Load()}
				if err != nil {
					payload["error"] = err.Error()
				}
				fmt.Fprint(w, "event: end\ndata: ")
				_ = enc.Encode(payload)
				fmt.Fprint(w, "\n")
				flusher.Flush()
				return
			}
			fmt.Fprint(w, "data: ")
			if err := enc.Encode(snap); err != nil {
				return
			}
			fmt.Fprint(w, "\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	ok := s.hub.stop(r.URL.Query().Get("id"))
	writeJSON(w, http.StatusOK, map[string]any{"stopped": ok})
}

func (s *Server) handleResult(w http.ResponseWriter, r *http.Request) {
	run, ok := s.hub.get(r.URL.Query().Get("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no such run"))
		return
	}
	res, done, err := run.snapshotResult()
	payload := map[string]any{"done": done, "result": res}
	if err != nil {
		payload["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, payload)
}

// --- benchmark comparison ---------------------------------------------------

type benchRequest struct {
	Scenario string   `json:"scenario"`
	Engines  []string `json:"engines"`
	Repeat   int      `json:"repeat"`
	Seed     *uint64  `json:"seed"`
	Ticks    *int     `json:"ticks"`
}

type benchRow struct {
	Engine     string  `json:"engine"`
	Variant    string  `json:"variant,omitempty"`
	WallMS     float64 `json:"wall_ms"`
	TicksPerS  float64 `json:"ticks_per_s"`
	Allocs     uint64  `json:"allocs"`
	HeapMB     float64 `json:"heap_mb"`
	Collected  int64   `json:"collected"`
	Checksum   string  `json:"checksum"`
	SpeedupX   float64 `json:"speedup_x"`
	Mismatched bool    `json:"mismatched"`
	Error      string  `json:"error,omitempty"`
}

// handleBench runs the selected engines HEADLESSLY (observer nil) and returns
// the comparison table.
//
// These numbers are indicative only, and the UI says so. They come from a
// process that is also serving HTTP, so the audit report must use hyperfine
// and benchstat instead. This endpoint exists to make an optimization's
// effect visible in seconds while you are working on it.
func (s *Server) handleBench(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("POST only"))
		return
	}
	var req benchRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := config.LoadScenario(req.Scenario)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Seed != nil {
		cfg.Seed = *req.Seed
	}
	if req.Ticks != nil {
		cfg.Ticks = *req.Ticks
	}
	if err := cfg.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := checkLimits(cfg); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Repeat < 1 {
		req.Repeat = 1
	}
	if req.Repeat > maxRepeat {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("repeat %d exceeds the server limit of %d", req.Repeat, maxRepeat))
		return
	}

	select {
	case s.benchSlot <- struct{}{}:
		defer func() { <-s.benchSlot }()
	default:
		w.Header().Set("Retry-After", "10")
		writeErr(w, http.StatusTooManyRequests, fmt.Errorf("another benchmark is running, retry shortly"))
		return
	}
	// A large scenario with repeats can outlast the server-wide WriteTimeout;
	// the bench's own budget bounds it instead.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(benchTimeout + 5*time.Second))

	engines := req.Engines
	if len(engines) == 0 {
		engines = simcore.Names()
	}

	ctx, cancel := context.WithTimeout(r.Context(), benchTimeout)
	defer cancel()
	rows := make([]benchRow, 0, len(engines))
	// Both the checksum reference and the speedup baseline are per VARIANT.
	// Engines implementing different simulations are not alternatives to each
	// other: calling one a speedup of the other would be meaningless, and
	// flagging their differing checksums as a mismatch would cry wolf on the
	// one signal that is supposed to mean "an optimization broke something".
	baseline := map[string]float64{}
	refChecksum := map[string]uint64{}

	for _, name := range engines {
		row := benchRow{Engine: name}
		eng, err := simcore.New(name)
		if err != nil {
			row.Error = err.Error()
			rows = append(rows, row)
			continue
		}

		best := simcore.Result{WallNS: 1<<63 - 1}
		var failed error
		for i := 0; i < req.Repeat; i++ {
			res, err := eng.Run(ctx, cfg, nil)
			if err != nil {
				failed = err
				break
			}
			// Keep the FASTEST run: slower ones are contaminated by whatever
			// else the machine was doing, and the minimum is the closest
			// estimate of the code's own cost.
			if res.WallNS < best.WallNS {
				best = res
			}
		}
		if failed != nil {
			row.Error = failed.Error()
			rows = append(rows, row)
			continue
		}

		row.WallMS = float64(best.WallNS) / 1e6
		if best.WallNS > 0 {
			row.TicksPerS = float64(best.TicksRun) / (float64(best.WallNS) / 1e9)
		}
		row.Allocs = best.Allocs
		row.HeapMB = float64(best.HeapBytes) / (1 << 20)
		row.Collected = best.FoodCollected
		row.Checksum = fmt.Sprintf("%#016x", best.StateChecksum)
		row.Variant = simcore.VariantOf(eng)

		// Within a variant, every engine must agree on the outcome. A mismatch
		// means an "optimization" changed the simulation, and its speed is
		// irrelevant.
		if ref, ok := refChecksum[row.Variant]; !ok {
			refChecksum[row.Variant] = best.StateChecksum
		} else if best.StateChecksum != ref {
			row.Mismatched = true
		}
		if baseline[row.Variant] == 0 {
			baseline[row.Variant] = row.WallMS
		}
		if row.WallMS > 0 {
			row.SpeedupX = baseline[row.Variant] / row.WallMS
		}
		rows = append(rows, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scenario":   req.Scenario,
		"seed":       cfg.Seed,
		"ticks":      cfg.Ticks,
		"ants":       cfg.AntCount,
		"repeat":     req.Repeat,
		"gomaxprocs": runtime.GOMAXPROCS(0),
		"rows":       rows,
		"note":       "Indicative only - this process is also serving HTTP. Use run_benchmarks.sh (hyperfine + benchstat) for the audit report.",
	})
}

// newHTTPServer applies the transport settings from the network course:
//
//   - HTTP/2 alongside HTTP/1.1, including cleartext HTTP/2 (h2c) for clients
//     that speak it with prior knowledge (curl --http2-prior-knowledge,
//     vegeta -h2c). Browsers only negotiate HTTP/2 over TLS, see Listen.
//   - A long IdleTimeout so keep-alive sockets are reused rather than paying
//     the TCP handshake again on every request.
//   - Read and write deadlines, so a stalled client cannot hold a goroutine
//     and a socket forever. The SSE and bench handlers lift the write
//     deadline themselves.
//   - BaseContext tied to ctx: on shutdown every request context is cancelled,
//     so open SSE streams return and running benches abort instead of making
//     Shutdown wait for them.
func (s *Server) newHTTPServer(ctx context.Context, addr string) *http.Server {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Server{
		Addr:              addr,
		Handler:           s,
		Protocols:         protocols,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
}

// Listen starts the server and blocks until ctx is cancelled. With a
// certificate and key it serves TLS, where browsers negotiate HTTP/2 and the
// SSE stream shares one multiplexed socket with the API calls instead of
// taking one of the six HTTP/1.1 connections a browser allows per origin.
func (s *Server) Listen(ctx context.Context, addr, certFile, keyFile string) error {
	srv := s.newHTTPServer(ctx, addr)
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	var err error
	if certFile != "" && keyFile != "" {
		err = srv.ListenAndServeTLS(certFile, keyFile)
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
