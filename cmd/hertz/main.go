// Command hertz runs one HERTZ SFU node: RTP ingest, WebRTC fanout, HTTP
// signaling, and Prometheus metrics.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pion/ice/v4"
	"hertz/internal/auth"
	"hertz/internal/config"
	"hertz/internal/metrics"
	"hertz/internal/rtpengine"
	"hertz/internal/sfu"
	"hertz/internal/signaling"
	"hertz/internal/version"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "err", err)
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	m := metrics.New()
	m.BuildInfo.WithLabelValues(version.Version, version.Commit).Set(1)

	hub := sfu.NewHub(sfu.HubConfig{
		MaxAverageBitrate: cfg.MaxBitrate,
		MaxHTTPListeners:  cfg.MaxHTTPListeners,
	})
	stream := hub.Ensure(cfg.StreamID)

	handler := rtpengine.NewHandler(rtpengine.HandlerConfig{
		StreamID:      cfg.StreamID,
		PayloadType:   cfg.IngestPayloadType,
		ExpectedSSRC:  cfg.IngestSSRC,
		MaxPacketSize: cfg.MaxPacketSize,
		MaxPPS:        cfg.IngestMaxPPS,
		JitterWindow:  cfg.JitterWindow,
		JitterTick:    cfg.JitterTick,
	}, log)
	handler.Start(stream.Broadcast)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var udpIngest *rtpengine.UDPIngest
	if cfg.IngestAddr != "" {
		udpIngest, err = rtpengine.NewUDPIngest(
			cfg.IngestAddr, handler, cfg.IngestAllowed,
			cfg.UDPReadBuffer, log,
		)
		if err != nil {
			log.Error("ingest startup failed", "err", err)
			os.Exit(1)
		}
		log.Info("RTP ingest listening", "addr", udpIngest.Addr().String(), "pt", cfg.IngestPayloadType)
	}

	var unixIngest *rtpengine.UnixIngest
	if cfg.IngestUnix != "" {
		unixIngest, err = rtpengine.NewUnixIngest(cfg.IngestUnix, handler, cfg.UDPReadBuffer, log)
		if err != nil {
			log.Error("unix ingest startup failed", "err", err)
			os.Exit(1)
		}
		log.Info("unixgram ingest listening", "path", cfg.IngestUnix)
	}
	if udpIngest != nil && unixIngest != nil {
		log.Warn("both UDP and unixgram ingest are enabled; use one source per stream")
	}
	if len(cfg.IngestAllowed) == 0 {
		log.Warn("no HERTZ_INGEST_ALLOWED_CIDRS set: UDP ingest accepts any source IP")
	}

	var mux ice.UDPMux
	if cfg.ICEUDPPort > 0 {
		opts := []ice.UDPMuxFromPortOption{ice.UDPMuxFromPortWithNetworks(ice.NetworkTypeUDP4)}
		if cfg.IncludeLoop {
			opts = append(opts, ice.UDPMuxFromPortWithLoopback())
		}
		multi, err := ice.NewMultiUDPMuxFromPort(cfg.ICEUDPPort, opts...)
		if err != nil {
			log.Error("ICE UDP mux startup failed", "err", err)
			os.Exit(1)
		}
		mux = multi
		log.Info("ICE UDP mux listening", "port", cfg.ICEUDPPort)
	} else {
		log.Warn("HERTZ_ICE_UDP_PORT is 0: using ephemeral ICE ports; open a firewall range in production")
	}

	signer, err := auth.NewSigner(cfg.AuthSecret, cfg.AllowInsecureDev)
	if err != nil {
		log.Error("auth setup failed", "err", err)
		os.Exit(1)
	}
	if cfg.AllowInsecureDev {
		log.Warn("insecure dev auth is enabled; token 'dev' is accepted. NEVER use this in production")
	}

	factory, err := sfu.NewPeerFactory(cfg, mux, m, log)
	if err != nil {
		log.Error("peer factory failed", "err", err)
		os.Exit(1)
	}
	mgr := sfu.NewManager(cfg, hub, factory, m, log)
	sig := signaling.NewServer(cfg, mgr, signer, m, log)

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           sig.Handler(),
		ReadHeaderTimeout: cfg.ReadHeaderLimit,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", m.Handler())
	if cfg.EnablePProf {
		log.Warn("pprof is enabled on the metrics listener; restrict it to a private interface")
		registerPProf(metricsMux)
	}
	metricsSrv := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 4)
	go func() {
		log.Info("signaling listening", "addr", cfg.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	go func() {
		log.Info("metrics listening", "addr", cfg.MetricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	if udpIngest != nil {
		go func() {
			if err := udpIngest.Run(ctx); err != nil {
				errCh <- err
			}
		}()
	}
	if unixIngest != nil {
		go func() {
			if err := unixIngest.Run(ctx); err != nil {
				errCh <- err
			}
		}()
	}

	go runIngestMetrics(ctx, m, handler, cfg.StreamID)
	go mgr.StartJanitor(ctx)
	go mgr.RunABR(ctx)

	select {
	case <-ctx.Done():
		log.Info("shutdown requested")
	case err := <-errCh:
		log.Error("fatal server error", "err", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	_ = metricsSrv.Shutdown(shutdownCtx)
	mgr.CloseAll()
	handler.Close()
	if udpIngest != nil {
		_ = udpIngest.Close()
	}
	if unixIngest != nil {
		_ = unixIngest.Close()
	}
	if mux != nil {
		_ = mux.Close()
	}
	log.Info("bye")
}

func runIngestMetrics(ctx context.Context, m *metrics.Metrics, h *rtpengine.PacketHandler, stream string) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var prev rtpengine.Snapshot
	var prevAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s := h.Snapshot()
			m.IngestPackets.WithLabelValues(stream).Add(float64(s.Packets - prev.Packets))
			m.IngestBytes.WithLabelValues(stream).Add(float64(s.Bytes - prev.Bytes))
			m.IngestLost.WithLabelValues(stream).Add(float64(s.Jitter.Lost - prev.Jitter.Lost))
			m.IngestLate.WithLabelValues(stream).Add(float64(s.Jitter.Late - prev.Jitter.Late))
			m.IngestDup.WithLabelValues(stream).Add(float64(s.Jitter.Duplicates - prev.Jitter.Duplicates))
			m.IngestInvalid.WithLabelValues(stream, "rtp").Add(float64(s.BadRTP - prev.BadRTP))
			m.IngestInvalid.WithLabelValues(stream, "payload_type").Add(float64(s.BadPT - prev.BadPT))
			m.IngestInvalid.WithLabelValues(stream, "ssrc").Add(float64(s.BadSSRC - prev.BadSSRC))
			m.IngestInvalid.WithLabelValues(stream, "source").Add(float64(s.BadSource - prev.BadSource))
			m.IngestInvalid.WithLabelValues(stream, "rate").Add(float64(s.RateDropped - prev.RateDropped))
			m.IngestJitter.WithLabelValues(stream).Set(s.Jitter.Jitter.Seconds())
			if !s.LastPacket.IsZero() {
				m.IngestLastSeen.WithLabelValues(stream).Set(float64(s.LastPacket.Unix()))
			}
			if !prevAt.IsZero() && s.Bytes >= prev.Bytes {
				dt := now.Sub(prevAt).Seconds()
				if dt > 0 {
					m.IngestBitrate.WithLabelValues(stream).Set(float64(s.Bytes-prev.Bytes) * 8 / dt)
				}
			}
			prev = s
			prevAt = now
		}
	}
}
