//go:build unix

package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// runCommand expects an exec.CommandContext command. Git, its hooks, and its
// transport helpers get their own process group so cancellation kills the group
// rather than only Git. Helpers that deliberately escape the group are outside
// this containment boundary; this is not a sandbox.
func runCommand(_ context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return cmd.Run()
}
