package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/myusuf3/commit/internal/cli"
	"golang.org/x/term"
)

// Populated by the release build with -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// interrupted is the cancellation cause recorded when a signal arrives.
type interrupted struct{ signal os.Signal }

func (i interrupted) Error() string { return "interrupted by " + i.signal.String() }

func main() {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-signals
		cancel(interrupted{sig})
		// Restore default handling: a second Ctrl-C terminates immediately.
		signal.Stop(signals)
	}()
	cmd := cli.NewRoot(cli.Options{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
		Interactive: term.IsTerminal(int(os.Stdin.Fd())),
		Build:       cli.Build{Version: version, Revision: commit, Date: date},
	})
	err := cmd.ExecuteContext(ctx)
	if err != nil && ctx.Err() == nil {
		// A terminal Ctrl-C reaches Git in the foreground group at the same
		// moment as this process; give signal delivery a moment to record it
		// so an interrupted Git command is reported as an interruption.
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	if code := report(os.Stderr, err, context.Cause(ctx)); code != 0 {
		os.Exit(code)
	}
}

// report prints the outcome and returns the exit status. Signals follow the
// shell convention of 128+signal (130 for Ctrl-C, 143 for SIGTERM) with a short
// notice instead of a raw "context canceled" error.
func report(stderr io.Writer, err, cause error) int {
	if err == nil {
		return 0
	}
	var sig interrupted
	if !errors.As(cause, &sig) {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	code, notice := 130, "\nInterrupted."
	if sig.signal == syscall.SIGTERM {
		code, notice = 143, "Terminated."
	}
	// Keep context that matters, such as "branch was pushed, but ...".
	if !errors.Is(err, context.Canceled) || err.Error() != context.Canceled.Error() {
		notice = fmt.Sprintf("%s (%v)", notice, err)
	}
	fmt.Fprintln(stderr, notice)
	return code
}
