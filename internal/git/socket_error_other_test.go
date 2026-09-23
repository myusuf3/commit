//go:build !windows

package git

import "syscall"

const socketResetError = syscall.ECONNRESET
