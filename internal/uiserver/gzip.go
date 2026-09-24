package uiserver

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// gzipPool reuses compressors: a gzip.Writer carries ~800 KB of internal
// state, and allocating one per request would put that on the GC every time.
var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
		return w
	},
}

// withGzip compresses responses for clients that accept it. Fewer bytes on
// the wire means fewer TCP segments, and on a high-RTT link fewer round trips
// before the congestion window lets the whole response through.
//
// The SSE stream is excluded: each frame must reach the browser the moment it
// is flushed, and a compressor buffering behind it would defeat that. Range
// requests are excluded because the byte offsets would refer to the
// uncompressed file.
func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if r.URL.Path == "/api/stream" ||
			r.Header.Get("Range") != "" ||
			!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(w)
		gw := &gzipResponseWriter{ResponseWriter: w, gz: gz}
		defer func() {
			if gw.compressing {
				_ = gz.Close()
			}
			gzipPool.Put(gz)
		}()
		next.ServeHTTP(gw, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	compressing bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	h := g.Header()
	// Bodiless responses and ones already encoded upstream pass through.
	if code != http.StatusNoContent && code != http.StatusNotModified && h.Get("Content-Encoding") == "" {
		h.Set("Content-Encoding", "gzip")
		// http.FileServer set the UNCOMPRESSED length; sending it would make
		// the client wait for bytes that never arrive.
		h.Del("Content-Length")
		g.compressing = true
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if !g.compressing {
		return g.ResponseWriter.Write(b)
	}
	return g.gz.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying connection, so
// handlers can still adjust their write deadline behind this middleware.
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }
