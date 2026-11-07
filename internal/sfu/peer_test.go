package sfu

import (
	"sync/atomic"
	"testing"
)

func TestSetOnCloseFiresOnceForAlreadyClosedPeer(t *testing.T) {
	var calls atomic.Int32
	p := &Peer{}
	p.closed.Store(true)
	p.SetOnClose(func() { calls.Add(1) })
	p.SetOnClose(func() { calls.Add(1) })
	p.fireOnClose()
	if got := calls.Load(); got != 1 {
		t.Fatalf("onClose calls=%d want 1", got)
	}
}
