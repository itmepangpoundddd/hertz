package rtpengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"
)

// UnixIngest reads SOCK_DGRAM RTP from a local Unix socket. This is the
// preferred ingest path for a mixer running on the same host: no UDP stack,
// no network ACL, and filesystem permissions provide publisher isolation.
//
// Linux is the production target. Windows supports AF_UNIX stream sockets but
// not necessarily SOCK_DGRAM; configure UDP ingest on Windows.
type UnixIngest struct {
	conn    *net.UnixConn
	path    string
	handler *PacketHandler
	log     *slog.Logger
}

// NewUnixIngest binds a unixgram socket at path with mode 0600.
func NewUnixIngest(path string, handler *PacketHandler, readBuffer int, log *slog.Logger) (*UnixIngest, error) {
	if dir := filepath.Dir(path); dir != "." {
		// A private parent directory closes the pre-chmod window even if the
		// platform ignores the temporary umask.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create socket directory: %w", err)
		}
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to remove non-socket %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	restoreUmask := privateUmask()
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	restoreUmask()
	if err != nil {
		return nil, fmt.Errorf("listen unixgram %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("chmod unixgram: %w", err)
	}
	if readBuffer > 0 {
		if err := conn.SetReadBuffer(readBuffer); err != nil {
			log.Warn("could not set unixgram read buffer", "err", err)
		}
	}
	return &UnixIngest{conn: conn, path: path, handler: handler, log: log}, nil
}

// Run blocks until ctx is cancelled or the socket is closed.
func (u *UnixIngest) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = u.conn.Close()
	}()

	buf := make([]byte, u.handler.cfg.MaxPacketSize+1)
	for {
		n, _, err := u.conn.ReadFromUnix(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			u.log.Warn("unixgram read error", "err", err)
			continue
		}
		u.handler.Handle(buf[:n], time.Now(), "unix")
	}
}

func (u *UnixIngest) Close() error {
	u.handler.Close()
	err := u.conn.Close()
	_ = os.Remove(u.path)
	return err
}
