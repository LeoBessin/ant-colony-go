// Command antweb serves the benchmark harness UI: a live view of the colony
// and a side-by-side engine comparison.
//
// This binary is NOT a measurement target. Numbers it reports come from a
// process that is simultaneously serving HTTP; the audit report's figures must
// come from cmd/antsim under hyperfine instead.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "antcolony/internal/engine"
	"antcolony/internal/uiserver"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	cert := flag.String("cert", "", "TLS certificate file (enables HTTPS and HTTP/2 for browsers)")
	key := flag.String("key", "", "TLS private key file")
	health := flag.Bool("healthcheck", false, "probe http://<addr>/healthz and exit 0 if healthy (for container healthchecks)")
	flag.Parse()

	if *health {
		os.Exit(probe(*addr))
	}

	// SIGTERM is what `docker stop` sends; SIGINT alone would leave the
	// container to be killed at the end of its grace period.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scheme := "http"
	if *cert != "" && *key != "" {
		scheme = "https"
	}
	srv := uiserver.New()
	// Credentials come from the environment, not flags, so they never show
	// up in `ps` or in the compose file. This is server configuration: the
	// simulation itself still reads no environment variable.
	if user, pass := os.Getenv("BASIC_AUTH_USER"), os.Getenv("BASIC_AUTH_PASSWORD"); pass != "" {
		if user == "" {
			user = "admin"
		}
		srv.RequireBasicAuth(user, pass)
		fmt.Fprintf(os.Stderr, "basic auth enabled for user %q\n", user)
	} else {
		fmt.Fprintln(os.Stderr, "WARNING: basic auth disabled (BASIC_AUTH_PASSWORD unset): the harness is open to anyone who can reach it")
	}

	fmt.Fprintf(os.Stderr, "ant colony harness: %s://%s\n", scheme, *addr)
	if err := srv.Listen(ctx, *addr, *cert, *key); err != nil {
		fmt.Fprintln(os.Stderr, "antweb:", err)
		os.Exit(1)
	}
}

// probe lets the distroless image check itself without shipping curl.
func probe(addr string) int {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}
