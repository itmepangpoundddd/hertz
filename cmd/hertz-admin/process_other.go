//go:build !windows

package main

import "os/exec"

func assignJobObject(cmd *exec.Cmd) error { return nil }

func closeJobObject() {}

func hideProcessWindow(cmd *exec.Cmd) {}
