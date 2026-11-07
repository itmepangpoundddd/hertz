//go:build !windows

package rtpengine

import "syscall"

// privateUmask narrows the process umask while the Unix socket is created.
// The window is very short and is restored immediately after bind. This
// prevents the socket from being created with a permissive mode before the
// explicit chmod(0600).
func privateUmask() func() {
	old := syscall.Umask(0o177)
	return func() { syscall.Umask(old) }
}
