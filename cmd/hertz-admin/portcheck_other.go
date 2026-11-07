//go:build !windows

package main

import "net"

func exclusiveListenConfig() *net.ListenConfig { return &net.ListenConfig{} }
