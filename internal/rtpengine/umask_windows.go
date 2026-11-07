//go:build windows

package rtpengine

// privateUmask is a no-op on Windows; AF_UNIX datagram sockets are not the
// production ingest path there.
func privateUmask() func() { return func() {} }
