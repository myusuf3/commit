//go:build unix

package git

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitCancellationKillsHookDescendants(t *testing.T) {
	dir := repository(t)
	stage(t, dir, "staged change\n")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMMIT_TEST_TREE_HELPER", "1")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	client := &Client{Dir: dir, Output: io.Discard}
	checkDescendantCancellation(t, false, func(ctx context.Context, address, marker string) error {
		script := fmt.Sprintf("#!/bin/sh\nexec %s -test.run=^TestCommandTreeHelper$ -- parent %s %s\n", quote(executable), quote(address), quote(marker))
		if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-commit"), []byte(script), 0700); err != nil {
			return err
		}
		return client.Commit(ctx, "test: cancelled commit")
	})
}
