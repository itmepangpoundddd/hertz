package sfu

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"hertz/internal/config"
	"hertz/internal/metrics"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	cfg := config.Config{
		MaxSessions:         2,
		MaxSessionsPerIP:    1,
		JoinRatePerSecond:   100,
		JoinBurst:           10,
		IPJoinRatePerSecond: 1000,
		IPJoinBurst:         1000,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewManager(cfg, nil, nil, metrics.New(), log)
}

func TestManagerRemoveBeforeRegistration(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewHub(HubConfig{MaxAverageBitrate: 256_000})
	stream := hub.Ensure("main")
	m := NewManager(config.Config{
		MaxSessions:         10,
		MaxSessionsPerIP:    10,
		JoinRatePerSecond:   100,
		JoinBurst:           10,
		IPJoinRatePerSecond: 100,
		IPJoinBurst:         10,
	}, hub, nil, metrics.New(), log)

	if err := m.reserve("10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	// This is the callback path when a peer closes before it is inserted into
	// m.peers. The admission slot and stream must not remain allocated.
	m.remove("never-registered", "10.0.0.1", "main")

	if got := m.Count(); got != 0 {
		t.Fatalf("manager count=%d want 0", got)
	}
	if got := stream.Count(); got != 0 {
		t.Fatalf("stream count=%d want 0", got)
	}
	if got := testutil.ToFloat64(m.metrics.SessionsActive); got != 0 {
		t.Fatalf("sessions_active=%v want 0", got)
	}
}

func TestManagerPerIPRateBeforeGlobal(t *testing.T) {
	cfg := config.Config{
		MaxSessions:         10,
		MaxSessionsPerIP:    10,
		JoinRatePerSecond:   0.001,
		JoinBurst:           2,
		IPJoinRatePerSecond: 0.001,
		IPJoinBurst:         1,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewManager(cfg, nil, nil, metrics.New(), log)

	if err := m.reserve("10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	// Same IP is rejected by the per-IP limiter before it can consume the
	// second global token.
	if err := m.reserve("10.0.0.1"); !errors.Is(err, ErrIPJoinRate) {
		t.Fatalf("second same-IP err=%v want ErrIPJoinRate", err)
	}
	// A different IP can still use the remaining global token.
	if err := m.reserve("10.0.0.2"); err != nil {
		t.Fatalf("different IP rejected after same-IP attack: %v", err)
	}
	m.release("10.0.0.1")
	m.release("10.0.0.2")
	// Disconnecting must not reset the per-IP burst immediately.
	if err := m.reserve("10.0.0.1"); !errors.Is(err, ErrIPJoinRate) {
		t.Fatalf("after release err=%v want ErrIPJoinRate", err)
	}
}

func TestManagerAdmissionLimits(t *testing.T) {
	m := testManager(t)
	if err := m.reserve("10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := m.reserve("10.0.0.1"); !errors.Is(err, ErrIPLimit) {
		t.Fatalf("second same-IP session err=%v want ErrIPLimit", err)
	}
	if err := m.reserve("10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := m.reserve("10.0.0.3"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("third session err=%v want ErrCapacity", err)
	}
	m.release("10.0.0.1")
	m.release("10.0.0.2")
	if m.Count() != 0 {
		t.Fatalf("count=%d want 0", m.Count())
	}
}
