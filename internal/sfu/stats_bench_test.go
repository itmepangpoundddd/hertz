package sfu

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"testing"

	"hertz/internal/config"
	"hertz/internal/metrics"
)

// BenchmarkPeerGetStats measures the server-side cost of pc.GetStats() for a
// batch of peer objects. Peers are created but not ICE-connected, so this is a
// lower bound for live sessions; the README must state that caveat.
//
//	HERTZ_BENCH_PEERS=1000 go test ./internal/sfu -run '^$' \
//	  -bench BenchmarkPeerGetStats -benchtime=1x -benchmem
func BenchmarkPeerGetStats(b *testing.B) {
	n := 100
	if raw := os.Getenv("HERTZ_BENCH_PEERS"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			b.Fatalf("bad HERTZ_BENCH_PEERS=%q", raw)
		}
		n = v
	}
	cfg := config.Config{
		QueueSize:           10,
		InitialBitrate:      256_000,
		MinBitrate:          96_000,
		MaxBitrate:          512_000,
		PacerMode:           "noop",
		StatsInterval:       5_000_000_000,
		IncludeLoop:         false,
		ICEUDPPort:          0,
		MaxSessions:         10_000,
		JoinRatePerSecond:   100,
		JoinBurst:           100,
		IPJoinRatePerSecond: 100,
		IPJoinBurst:         100,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := metrics.New()
	factory, err := NewPeerFactory(cfg, nil, m, log)
	if err != nil {
		b.Fatal(err)
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	peers := make([]*Peer, 0, n)
	for i := 0; i < n; i++ {
		p, err := factory.New(fmt.Sprintf("bench-%d", i), "main", "bench", "127.0.0.1")
		if err != nil {
			b.Fatalf("peer %d: %v", i, err)
		}
		peers = append(peers, p)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	heapPerPeer := int64(0)
	if after.HeapAlloc > before.HeapAlloc {
		heapPerPeer = int64(after.HeapAlloc-before.HeapAlloc) / int64(n)
	}
	b.ReportMetric(float64(heapPerPeer), "heap-B/peer")
	b.ReportMetric(float64(n), "peers")
	b.Logf("heap before=%d after=%d per-peer=%d", before.HeapAlloc, after.HeapAlloc, heapPerPeer)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range peers {
			_ = p.pc.GetStats()
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/getstats-per-peer")

	if path := os.Getenv("HERTZ_HEAP_PROFILE"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			b.Fatal(err)
		}
		runtime.GC()
		if err := pprof.WriteHeapProfile(f); err != nil {
			_ = f.Close()
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}

	for _, p := range peers {
		p.Close()
	}
}
