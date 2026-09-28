//go:build unix

package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// runCommand expects an exec.CommandContext command.
//
// A command that may need the user's terminal (SSH passphrases and host-key
// confirmation, HTTPS credential prompts, interactive hooks, commit signing)
// stays in the caller's foreground process group. A separate process group
// would be a background job: reading the terminal would stop it with SIGTTIN
// until the timeout killed it. Terminal Ctrl-C already signals the whole
// foreground group, including hooks and helpers; a deadline kills Git itself.
//
// Every other command runs in its own session. That detaches it from the
// controlling terminal, so an unexpected prompt fails immediately instead of
// hanging, and cancellation kills the whole process group rather than only
// Git. Helpers that deliberately escape the group are outside this
// containment boundary; this is not a sandbox.
func runCommand(_ context.Context, cmd *exec.Cmd, terminal bool) error {
	if terminal {
		return cmd.Run()
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return cmd.Run()
}
