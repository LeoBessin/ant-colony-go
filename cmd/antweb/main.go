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
	"os"
	"os/signal"

	_ "antcolony/internal/engine"
	"antcolony/internal/uiserver"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Fprintf(os.Stderr, "ant colony harness: http://%s\n", *addr)
	if err := uiserver.New().Listen(ctx, *addr); err != nil {
		fmt.Fprintln(os.Stderr, "antweb:", err)
		os.Exit(1)
	}
}
