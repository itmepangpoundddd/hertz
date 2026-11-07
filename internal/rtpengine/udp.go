package rtpengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// UDPIngest reads RTP datagrams from one UDP socket.
type UDPIngest struct {
	conn    *net.UDPConn
	handler *PacketHandler
	log     *slog.Logger
	allowed []*net.IPNet
}

// NewUDPIngest binds addr and configures a large kernel receive buffer. A
// dedicated socket per source is intentional: it keeps the hot path free of
// stream lookup and makes ingress ACLs simple.
func NewUDPIngest(addr string, handler *PacketHandler, allowed []*net.IPNet, readBuffer int, log *slog.Logger) (*UDPIngest, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve ingest addr: %w", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("listen ingest %s: %w", addr, err)
	}
	if readBuffer > 0 {
		if err := conn.SetReadBuffer(readBuffer); err != nil {
			log.Warn("could not set UDP read buffer", "err", err, "requested", readBuffer)
		}
	}
	return &UDPIngest{
		conn:    conn,
		handler: handler,
		log:     log,
		allowed: allowed,
	}, nil
}

// Run blocks until ctx is cancelled or the socket is closed. Handle is
// synchronous and copies retained packets, so one read buffer is sufficient.
func (u *UDPIngest) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = u.conn.Close()
	}()

	buf := make([]byte, u.handler.cfg.MaxPacketSize+1)
	backoff := 10 * time.Millisecond
	for {
		n, src, err := u.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			u.log.Warn("ingest read error", "err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff < time.Second {
				backoff *= 2
				if backoff > time.Second {
					backoff = time.Second
				}
			}
			continue
		}
		backoff = 10 * time.Millisecond

		ip := net.IP(src.Addr().Unmap().AsSlice())
		if !u.sourceAllowed(ip) {
			u.handler.RejectSource()
			continue
		}
		u.handler.Handle(buf[:n], time.Now(), src.String())
	}
}

// Addr returns the bound address (useful when :0 is configured in tests).
func (u *UDPIngest) Addr() net.Addr { return u.conn.LocalAddr() }

func (u *UDPIngest) Close() error {
	u.handler.Close()
	return u.conn.Close()
}

func (u *UDPIngest) sourceAllowed(ip net.IP) bool {
	if len(u.allowed) == 0 {
		return true
	}
	for _, n := range u.allowed {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
