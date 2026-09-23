package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/myusuf3/commit/internal/cli"
	"golang.org/x/term"
)

// Populated by the release build with -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := cli.NewRoot(cli.Options{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
		Interactive: term.IsTerminal(int(os.Stdin.Fd())),
		Build:       cli.Build{Version: version, Revision: commit, Date: date},
	})
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		stop()
		os.Exit(1)
	}
}
