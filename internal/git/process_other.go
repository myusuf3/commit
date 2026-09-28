//go:build !unix && !windows

package git

import (
	"context"
	"errors"
	"os/exec"
)

func runCommand(context.Context, *exec.Cmd, bool) error {
	return errors.New("Git process-tree cancellation is not supported on this platform")
}
