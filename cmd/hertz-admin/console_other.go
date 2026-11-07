//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

func configureConsole() {}

func hideConsole() {}

func showConsole() {}

func consoleVisible() bool { return true }

func messageBox(title, text string, isError bool) {
	fmt.Printf("%s: %s\n", title, text)
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func openFolder(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

func copyToClipboard(text string) error {
	return errors.New("clipboard is not supported on this platform; use the dashboard copy button")
}
