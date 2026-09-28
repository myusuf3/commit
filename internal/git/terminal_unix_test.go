//go:build unix

package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestProcessGroupHelper(t *testing.T) {
	if os.Getenv("COMMIT_TEST_PGRP_HELPER") != "1" {
		t.Skip("helper process")
	}
	sid, _ := unix.Getsid(0)
	fmt.Printf("%d %d %d\n", os.Getpid(), syscall.Getpgrp(), sid)
	os.Exit(0)
}

// A background process group is stopped by SIGTTIN when it reads the terminal,
// so terminal commands must stay in the caller's (foreground) group, while
// other commands are detached into their own session and cannot prompt.
func TestTerminalCommandsStayInForegroundGroup(t *testing.T) {
	t.Setenv("COMMIT_TEST_PGRP_HELPER", "1")
	for _, terminal := range []bool{false, true} {
		cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestProcessGroupHelper$")
		var out strings.Builder
		cmd.Stdout = &out
		if err := runCommand(context.Background(), cmd, terminal); err != nil {
			t.Fatal(err)
		}
		var pid, pgrp, sid int
		if _, err := fmt.Sscan(out.String(), &pid, &pgrp, &sid); err != nil {
			t.Fatalf("output %q: %v", out.String(), err)
		}
		parentSid, _ := unix.Getsid(0)
		if terminal && (pgrp != syscall.Getpgrp() || sid != parentSid) {
			t.Fatalf("terminal command left the foreground group: pgrp=%d sid=%d", pgrp, sid)
		}
		if !terminal && (pgrp != pid || sid != pid) {
			t.Fatalf("detached command shares a group or session: pid=%d pgrp=%d sid=%d", pid, pgrp, sid)
		}
	}
}
