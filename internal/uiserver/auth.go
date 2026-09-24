package uiserver

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
)

var errUnauthorized = errors.New("authentication required")

// RequireBasicAuth puts the whole harness behind HTTP Basic authentication,
// except /healthz: the container healthcheck probes it from inside the
// container, with no credentials.
//
// It lives in the application rather than in the reverse proxy so it holds
// whatever sits in front (Traefik, Caddy, nothing) and covers every domain
// routed to the container. Browsers resend the credentials on every
// same-origin request, EventSource included, so the SSE stream keeps working.
func (s *Server) RequireBasicAuth(user, password string) {
	wantUser := sha256.Sum256([]byte(user))
	wantPass := sha256.Sum256([]byte(password))
	next := s.handler
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		// Compare fixed-size digests in constant time: comparing the raw
		// strings would leak the length, and == would leak the matching
		// prefix through timing.
		gotUser := sha256.Sum256([]byte(u))
		gotPass := sha256.Sum256([]byte(p))
		userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:])
		passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:])
		if !ok || userOK&passOK != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="ant colony harness", charset="UTF-8"`)
			writeErr(w, http.StatusUnauthorized, errUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
