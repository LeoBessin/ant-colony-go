package uiserver

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
