package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Start suspended, attach to a non-breakaway Job Object, then resume. Assigning
// a job after an ordinary Start would race Git spawning uncontained helpers.
// Job termination and kill-on-close cover the process and its descendants.
func runCommand(ctx context.Context, cmd *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create Git process job: %w", err)
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	// This Win32 wrapper takes uintptr, not a tracked Go pointer. Keep the
	// structure pinned across the call (including any lazy DLL resolution).
	var pinned runtime.Pinner
	pinned.Pin(&limits)
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	pinned.Unpin()
	if err != nil {
		return fmt.Errorf("configure Git process job: %w", err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	cmd.Cancel = func() error {
		jobErr := windows.TerminateJobObject(job, 1)
		// Cancellation may arrive before assignment; Git is still suspended then,
		// so killing the direct process cannot leave any uncontained descendants.
		processErr := cmd.Process.Kill()
		if jobErr != nil {
			return errors.Join(jobErr, processErr)
		}
		return nil
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	abort := func(cause error) error {
		_ = windows.TerminateJobObject(job, 1)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return cause
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return abort(fmt.Errorf("open suspended Git process: %w", err))
	}
	err = windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if err != nil {
		return abort(fmt.Errorf("contain Git process: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return abort(err)
	}
	if err := resumeProcess(uint32(cmd.Process.Pid)); err != nil {
		return abort(err)
	}
	return cmd.Wait()
}

// os/exec closes the primary thread handle. Enumerate the suspended process's
// threads to reopen and resume its startup thread using documented Win32 APIs.
func resumeProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("inspect suspended Git process: %w", err)
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return fmt.Errorf("open Git startup thread: %w", err)
		}
		count, err := windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if err != nil {
			return fmt.Errorf("resume Git startup thread: %w", err)
		}
		if count > 0 {
			return nil
		}
	}
	return errors.New("could not find the suspended Git startup thread")
}
