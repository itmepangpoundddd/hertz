//go:build windows

package main

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procGetConsoleWindow   = kernel32.NewProc("GetConsoleWindow")
	procSetConsoleOutputCP = kernel32.NewProc("SetConsoleOutputCP")
	procSetConsoleCP       = kernel32.NewProc("SetConsoleCP")
	procSetConsoleTitleW   = kernel32.NewProc("SetConsoleTitleW")
	procShowWindow         = user32.NewProc("ShowWindow")
	procIsWindowVisible    = user32.NewProc("IsWindowVisible")
	procMessageBoxW        = user32.NewProc("MessageBoxW")
	procSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")

	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")

	procGlobalAlloc   = kernel32.NewProc("GlobalAlloc")
	procGlobalLock    = kernel32.NewProc("GlobalLock")
	procGlobalUnlock  = kernel32.NewProc("GlobalUnlock")
	procGlobalFree    = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")

	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

const (
	swHide = 0
	swShow = 5

	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	mbOK              = 0x00000000
	mbIconError       = 0x00000010
	mbIconInformation = 0x00000040
	mbSetForeground   = 0x00010000
	mbTopMost         = 0x00040000
)

func configureConsole() {
	// Go's default manifest is used so external-linker builds (go test -race)
	// do not conflict with a second embedded manifest. DPI awareness is set at
	// runtime instead.
	_, _, _ = procSetProcessDPIAware.Call()
	const utf8CodePage = 65001
	_, _, _ = procSetConsoleOutputCP.Call(utf8CodePage)
	_, _, _ = procSetConsoleCP.Call(utf8CodePage)
	if title, err := windows.UTF16PtrFromString("HERTZ — Live audio streaming"); err == nil {
		_, _, _ = procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(title)))
	}
}

func consoleWindow() uintptr {
	hwnd, _, _ := procGetConsoleWindow.Call()
	return hwnd
}

func hideConsole() {
	if hwnd := consoleWindow(); hwnd != 0 {
		_, _, _ = procShowWindow.Call(hwnd, swHide)
	}
}

func showConsole() {
	if hwnd := consoleWindow(); hwnd != 0 {
		_, _, _ = procShowWindow.Call(hwnd, swShow)
	}
}

func consoleVisible() bool {
	hwnd := consoleWindow()
	if hwnd == 0 {
		return false
	}
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	return visible != 0
}

func messageBox(title, text string, isError bool) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	flags := uintptr(mbOK | mbSetForeground | mbTopMost)
	if isError {
		flags |= mbIconError
	} else {
		flags |= mbIconInformation
	}
	_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), flags)
}

func shellOpen(target string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(target)
	ret, _, err := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0,
		0,
		1, // SW_SHOWNORMAL
	)
	if ret <= 32 {
		if err != nil && !errors.Is(err, windows.ERROR_SUCCESS) {
			return fmt.Errorf("ShellExecuteW: %w", err)
		}
		return fmt.Errorf("ShellExecuteW failed with code %d", ret)
	}
	return nil
}

func openBrowser(url string) error {
	return shellOpen(url)
}

func openFolder(path string) error {
	return shellOpen(path)
}

func copyToClipboard(text string) error {
	if r, _, err := procOpenClipboard.Call(0); r == 0 {
		return fmt.Errorf("OpenClipboard: %w", err)
	}
	defer func() { _, _, _ = procCloseClipboard.Call() }()
	if r, _, err := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("EmptyClipboard: %w", err)
	}

	utf16, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	size := uintptr(len(utf16) * 2)
	h, _, allocErr := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return fmt.Errorf("GlobalAlloc: %w", allocErr)
	}
	ptr, _, lockErr := procGlobalLock.Call(h)
	if ptr == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return fmt.Errorf("GlobalLock: %w", lockErr)
	}
	// Copy through RtlMoveMemory instead of converting the uintptr returned by
	// GlobalLock back into an unsafe.Pointer, which go vet cannot verify.
	_, _, _ = procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&utf16[0])), size)
	_, _, _ = procGlobalUnlock.Call(h)

	if r, _, setErr := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return fmt.Errorf("SetClipboardData: %w", setErr)
	}
	// Ownership of h moved to the clipboard.
	return nil
}
