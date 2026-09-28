package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestReport(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err, cause error
		code       int
		want       string
	}{
		{"success", nil, nil, 0, ""},
		{"success despite signal", nil, interrupted{os.Interrupt}, 0, ""},
		{"error", errors.New("boom"), nil, 1, "Error: boom\n"},
		{"ctrl-c", context.Canceled, interrupted{os.Interrupt}, 130, "\nInterrupted.\n"},
		{"sigterm", context.Canceled, interrupted{syscall.SIGTERM}, 143, "Terminated.\n"},
		{"ctrl-c keeps context", fmt.Errorf("branch was pushed, but the pull request operation failed: %w", context.Canceled), interrupted{os.Interrupt}, 130,
			"\nInterrupted. (branch was pushed, but the pull request operation failed: context canceled)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := report(&out, tc.err, tc.cause); code != tc.code || out.String() != tc.want {
				t.Fatalf("code=%d output=%q", code, out.String())
			}
		})
	}
}
