//go:build windows

package main

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	jobOnce   sync.Once
	jobHandle windows.Handle
	jobErr    error
)

// initJobObject creates a job object whose handle is held for the lifetime of
// the admin process. JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE makes Windows kill the
// engine when the admin process exits, even if it crashes or is killed from
// Task Manager. This prevents a hidden engine from holding ports forever.
func initJobObject() error {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			jobErr = err
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(
			h,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); err != nil {
			_ = windows.CloseHandle(h)
			jobErr = err
			return
		}
		jobHandle = h
	})
	return jobErr
}

func assignJobObject(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := initJobObject(); err != nil {
		return err
	}
	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(jobHandle, h)
}

func closeJobObject() {
	if jobHandle != 0 {
		_ = windows.CloseHandle(jobHandle)
		jobHandle = 0
	}
}

func hideProcessWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}
