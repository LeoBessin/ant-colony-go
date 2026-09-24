package uiserver

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"antcolony/internal/config"
)

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz: status %d", rec.Code)
	}
}

func TestGzipJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/meta", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.NewDecoder(zr).Decode(&meta); err != nil {
		t.Fatalf("decoding gzipped /api/meta: %v", err)
	}
	if _, ok := meta["engines"]; !ok {
		t.Fatalf("/api/meta missing engines: %v", meta)
	}
}

func TestGzipStaticDropsContentLength(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("static asset not compressed")
	}
	if cl := rec.Header().Get("Content-Length"); cl != "" {
		t.Fatalf("Content-Length %s leaked through (uncompressed length)", cl)
	}
}

// The SSE stream must never be compressed: frames would sit in the
// compressor's buffer instead of reaching the browser as they are flushed.
func TestStreamNotGzipped(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/stream?id=missing", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("/api/stream Content-Encoding = %q, want none", got)
	}
}

func TestCleartextHTTP2(t *testing.T) {
	s := New()
	ts := httptest.NewUnstartedServer(nil)
	ts.Config = s.newHTTPServer(context.Background(), "")
	ts.Start()
	defer ts.Close()

	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{Protocols: protocols}}

	resp, err := client.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("negotiated %s, want HTTP/2 over cleartext", resp.Proto)
	}
}

func post(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

// A public harness must refuse requests that would exhaust the host.
func TestLimitsRejectOversizedRequests(t *testing.T) {
	cases := []struct{ name, path, body, want string }{
		{"huge ticks", "/api/run", `{"scenario":"tiny","engine":"naive","ticks":1000000000}`, "server limit"},
		{"huge ants", "/api/run", `{"scenario":"tiny","engine":"naive","ants":1000000}`, "server limit"},
		{"huge grid", "/api/run", `{"engine":"naive","config":{"version":1,"width":100000,"height":100000,"ticks":1,"ant_count":1,"nest":{"x":0,"y":0},"pheromone":{"evap_den":1,"diff_den":1,"max":1},"movement":{"random_den":1}}}`, "server limit"},
		{"overflowing grid", "/api/run", `{"engine":"naive","config":{"version":1,"width":4294967296,"height":4294967296,"ticks":1,"ant_count":1,"nest":{"x":0,"y":0},"pheromone":{"evap_den":1,"diff_den":1,"max":1},"movement":{"random_den":1}}}`, "server limit"},
		{"ant-tick budget", "/api/run", `{"scenario":"large","engine":"parallel","ants":2000,"ticks":5000}`, "ant_count x ticks"},
		{"ant-tick budget bench", "/api/bench", `{"scenario":"large","engines":["parallel"],"ticks":5000}`, "ant_count x ticks"},
		{"huge repeat", "/api/bench", `{"scenario":"tiny","engines":["naive"],"repeat":1000}`, "server limit"},
		{"huge bench ticks", "/api/bench", `{"scenario":"tiny","engines":["naive"],"ticks":1000000000}`, "server limit"},
		{"oversized body", "/api/run", `{"scenario":"tiny","engine":"naive","pad":"` + strings.Repeat("x", maxBodyBytes) + `"}`, "too large"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := post(t, New(), c.path, c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
			}
			// The refusal must come from the caps, not from an unrelated
			// validation error in the test's own payload.
			if !strings.Contains(rec.Body.String(), c.want) {
				t.Fatalf("error %q does not mention %q", rec.Body, c.want)
			}
		})
	}
}

// Every embedded scenario must stay runnable under the caps.
func TestLimitsAdmitEmbeddedScenarios(t *testing.T) {
	for _, name := range config.ScenarioNames() {
		cfg, err := config.LoadScenario(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkLimits(cfg); err != nil {
			t.Errorf("scenario %s refused: %v", name, err)
		}
	}
}

func TestBenchIsSerialized(t *testing.T) {
	s := New()
	s.benchSlot <- struct{}{} // a bench is already running
	rec := post(t, s, "/api/bench", `{"scenario":"tiny","engines":["naive"]}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", rec.Code)
	}
	<-s.benchSlot
	if rec := post(t, s, "/api/bench", `{"scenario":"tiny","engines":["naive"],"ticks":5}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d after the slot freed: %s", rec.Code, rec.Body)
	}
}

func TestBasicAuth(t *testing.T) {
	s := New()
	s.RequireBasicAuth("leo", "s3cret")

	get := func(path, user, pass string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if user != "" || pass != "" {
			req.SetBasicAuth(user, pass)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}

	for _, c := range []struct{ name, user, pass string }{
		{"no credentials", "", ""},
		{"wrong password", "leo", "nope"},
		{"wrong user", "admin", "s3cret"},
		{"password prefix", "leo", "s3cre"},
	} {
		rec := get("/", c.user, c.pass)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", c.name, rec.Code)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: no WWW-Authenticate header, the browser would not prompt", c.name)
		}
	}
	if rec := get("/api/meta", "leo", "s3cret"); rec.Code != http.StatusOK {
		t.Errorf("valid credentials: status %d", rec.Code)
	}
	// The container healthcheck probes without credentials.
	if rec := get("/healthz", "", ""); rec.Code != http.StatusOK {
		t.Errorf("/healthz behind auth: status %d", rec.Code)
	}
}

// Unlimited mode (local load testing) admits what the caps refuse, and
// lets benches run concurrently.
func TestDisableLimits(t *testing.T) {
	s := New()
	s.DisableLimits()
	if rec := post(t, s, "/api/bench", `{"scenario":"tiny","engines":["naive"],"repeat":6,"ticks":5}`); rec.Code != http.StatusOK {
		t.Fatalf("repeat above cap: status %d: %s", rec.Code, rec.Body)
	}
	s.benchSlot <- struct{}{} // would mean "a bench is running" in limited mode
	if rec := post(t, s, "/api/bench", `{"scenario":"tiny","engines":["naive"],"ticks":5}`); rec.Code != http.StatusOK {
		t.Fatalf("concurrent bench: status %d: %s", rec.Code, rec.Body)
	}
	// No engine is registered in this package's tests, so the run itself
	// fails; what matters is that the caps no longer refuse it.
	if rec := post(t, s, "/api/run", `{"scenario":"tiny","engine":"naive","ticks":6000}`); strings.Contains(rec.Body.String(), "server limit") {
		t.Fatalf("ticks above cap still refused: %s", rec.Body)
	}
}
