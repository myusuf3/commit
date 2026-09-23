package git

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Re-exec the test binary, not a platform-specific shell, to exercise nested
// processes on Windows as well as Unix. Only a loopback socket is used.
func TestCommandTreeHelper(t *testing.T) {
	if os.Getenv("COMMIT_TEST_TREE_HELPER") != "1" {
		return
	}
	if len(os.Args) < 3 {
		t.Fatal("missing helper arguments")
	}
	args := os.Args[len(os.Args)-3:]
	role, address, marker := args[0], args[1], args[2]
	switch role {
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestCommandTreeHelper$", "--", "child", address, marker)
		if err := child.Run(); err != nil {
			t.Fatal(err)
		}
	case "child":
		conn, err := net.DialTimeout("tcp", address, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		// The child keeps its socket open while blocked here. EOF observed by
		// the test after cancellation proves the descendant was terminated.
		if _, err := conn.Read(make([]byte, 1)); err == nil {
			if err := os.WriteFile(marker, []byte("unwanted side effect"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	default:
		t.Fatalf("unknown helper role %q", role)
	}
}

func checkDescendantCancellation(t *testing.T, timeout bool, run func(context.Context, string, string) error) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "side-effect")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	limit := 20 * time.Second
	if timeout {
		limit = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, listener.Addr().String(), marker) }()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("descendant did not start: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if !timeout {
		cancel()
	}
	select {
	case err := <-done:
		if err == nil || ctx.Err() == nil {
			t.Fatalf("command did not report cancellation: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("command did not return on cancellation")
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("descendant survived cancellation (expected socket closure): %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("descendant performed a side effect")
	}
}

func TestRunCommandKillsDescendants(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "cancel"
		if timeout {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("COMMIT_TEST_TREE_HELPER", "1")
			checkDescendantCancellation(t, timeout, func(ctx context.Context, address, marker string) error {
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCommandTreeHelper$", "--", "parent", address, marker)
				cmd.WaitDelay = time.Second
				return runCommand(ctx, cmd)
			})
		})
	}
}

func TestRunCommandAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	if err := runCommand(ctx, cmd); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if cmd.Process != nil {
		t.Fatal("started a process after cancellation")
	}
}
