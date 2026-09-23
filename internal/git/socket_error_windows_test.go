package git

import "golang.org/x/sys/windows"

// Windows sockets return WinSock errors, not syscall's synthetic POSIX errno.
const socketResetError = windows.WSAECONNRESET
