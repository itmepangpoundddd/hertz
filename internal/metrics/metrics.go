// Package metrics defines the Prometheus surface for HERTZ.
//
// Cardinality rule: stream IDs are labels; listener/session IDs are never
// labels. Per-listener values are observed as histograms instead.
package metrics

import (
	"net/http"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

// Metrics owns a private registry so tests and multiple instances do not
// collide on the global default registry.
type Metrics struct {
	Registry *prometheus.Registry

	IngestPackets  *prometheus.CounterVec
	IngestBytes    *prometheus.CounterVec
	IngestInvalid  *prometheus.CounterVec
	IngestLost     *prometheus.CounterVec
	IngestLate     *prometheus.CounterVec
	IngestDup      *prometheus.CounterVec
	IngestJitter   *prometheus.GaugeVec
	IngestBitrate  *prometheus.GaugeVec
	IngestLastSeen *prometheus.GaugeVec
	SourceBitrate  *prometheus.GaugeVec

	Listeners      *prometheus.GaugeVec
	SessionsActive prometheus.Gauge
	SessionsTotal  prometheus.Counter
	SessionErrors  *prometheus.CounterVec
	JoinSeconds    prometheus.Histogram
	FanoutDropped  *prometheus.CounterVec
	FanoutWritten  *prometheus.CounterVec

	TargetBitrate *prometheus.HistogramVec
	RTT           *prometheus.HistogramVec
	PacketLoss    *prometheus.HistogramVec
	Jitter        *prometheus.HistogramVec
	OutboundRate  *prometheus.HistogramVec
	FreezeSeconds *prometheus.HistogramVec

	BuildInfo *prometheus.GaugeVec
}

// New builds and registers all collectors.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	f := promauto.With(reg)

	m := &Metrics{
		Registry: reg,
		IngestPackets: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hertz_ingest_packets_total",
			Help: "Valid RTP packets accepted from the source.",
		}, []string{"stream"}),
		IngestBytes: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hertz_ingest_bytes_total",
			Help: "RTP bytes accepted from the source.",
		}, []string{"stream"}),
		IngestInvalid: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hertz_ingest_invalid_packets_total",
			Help: "RTP packets rejected before the jitter buffer.",
		}, []string{"stream", "reason"}),
		IngestLost: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hertz_ingest_lost_packets_total",
			Help: "Packets inferred lost by sequence gaps in the ingest jitter buffer.",
		}, []string{"stream"}),
		IngestLate: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hertz_ingest_late_packets_total",
			Help: "Packets arriving after their playout position.",
		}, []string{"stream"}),
		IngestDup: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hertz_ingest_duplicate_packets_total",
			Help: "Duplicate RTP packets dropped by the ingest jitter buffer.",
		}, []string{"stream"}),
		IngestJitter: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hertz_ingest_jitter_seconds",
			Help: "RFC 3550 interarrival jitter measured at ingest.",
		}, []string{"stream"}),
		IngestBitrate: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hertz_ingest_bitrate_bps",
			Help: "Ingest payload bitrate in bits per second.",
		}, []string{"stream"}),
		IngestLastSeen: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hertz_ingest_last_packet_seconds",
			Help: "Unix seconds of the last valid ingest packet.",
		}, []string{"stream"}),
		SourceBitrate: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hertz_source_target_bitrate_bps",
			Help: "Bitrate requested from the source encoder by the ABR policy.",
		}, []string{"stream"}),

		Listeners: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hertz_listeners",
			Help: "Currently attached listeners per stream.",
		}, []string{"stream"}),
		SessionsActive: f.NewGauge(prometheus.GaugeOpts{
			Name: "hertz_sessions_active",
			Help: "Active WebRTC subscriber sessions.",
		}),
		SessionsTotal: f.NewCounter(prometheus.CounterOpts{
			Name: "hertz_sessions_total",
			Help: "Subscriber sessions created since process start.",
		}),
		SessionErrors: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hertz_session_errors_total",
			Help: "Subscriber session failures by reason.",
		}, []string{"reason"}),
		JoinSeconds: f.NewHistogram(prometheus.HistogramOpts{
			Name:    "hertz_session_join_seconds",
			Help:    "Time from HTTP subscribe request to SDP answer.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
		}),
		FanoutDropped: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hertz_fanout_dropped_packets_total",
			Help: "Packets dropped per listener because the output queue was full.",
		}, []string{"stream"}),
		FanoutWritten: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hertz_fanout_written_packets_total",
			Help: "Packets written to listener peer connections.",
		}, []string{"stream"}),

		TargetBitrate: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hertz_listener_target_bitrate_bps",
			Help:    "GCC target bitrate observed per listener.",
			Buckets: []float64{32e3, 64e3, 96e3, 128e3, 192e3, 256e3, 384e3, 512e3, 1e6},
		}, []string{"stream"}),
		RTT: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hertz_listener_rtt_seconds",
			Help:    "Subscriber RTT reported by RTCP.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.2, 0.3, 0.5, 1, 2},
		}, []string{"stream"}),
		PacketLoss: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hertz_listener_packet_loss_ratio",
			Help:    "Subscriber packet loss ratio reported by RTCP.",
			Buckets: []float64{0, 0.001, 0.005, 0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1},
		}, []string{"stream"}),
		Jitter: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hertz_listener_jitter_seconds",
			Help:    "Subscriber jitter reported by RTCP.",
			Buckets: []float64{0, 0.002, 0.005, 0.01, 0.02, 0.03, 0.05, 0.1, 0.2},
		}, []string{"stream"}),
		OutboundRate: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hertz_listener_outbound_bitrate_bps",
			Help:    "Actual RTP send rate per listener.",
			Buckets: []float64{16e3, 32e3, 64e3, 96e3, 128e3, 192e3, 256e3, 384e3, 512e3},
		}, []string{"stream"}),
		FreezeSeconds: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hertz_listener_no_packet_seconds",
			Help:    "Time since the last packet was written to a listener when sampled.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		}, []string{"stream"}),
		BuildInfo: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hertz_build_info",
			Help: "Build information; value is always 1.",
		}, []string{"version", "commit"}),
	}

	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler returns the /metrics HTTP handler.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// GaugeValue reads one gauge without going through the full Prometheus text
// exposition. Used by the public status endpoint.
func GaugeValue(g prometheus.Gauge) float64 {
	if g == nil {
		return 0
	}
	var out dto.Metric
	if err := g.Write(&out); err != nil {
		return 0
	}
	return out.GetGauge().GetValue()
}

// PeerSample is the per-listener snapshot consumed by ObservePeer.
type PeerSample struct {
	RTT           time.Duration
	Jitter        time.Duration
	Loss          float64
	TargetBitrate int
	OutboundBPS   int64
	NoPacketFor   time.Duration
}

// ObservePeer records one listener sample.
func (m *Metrics) ObservePeer(stream string, p PeerSample) {
	if p.TargetBitrate > 0 {
		m.TargetBitrate.WithLabelValues(stream).Observe(float64(p.TargetBitrate))
	}
	if p.RTT > 0 {
		m.RTT.WithLabelValues(stream).Observe(p.RTT.Seconds())
	}
	if p.Loss >= 0 {
		m.PacketLoss.WithLabelValues(stream).Observe(p.Loss)
	}
	if p.Jitter > 0 {
		m.Jitter.WithLabelValues(stream).Observe(p.Jitter.Seconds())
	}
	if p.OutboundBPS > 0 {
		m.OutboundRate.WithLabelValues(stream).Observe(float64(p.OutboundBPS))
	}
	if p.NoPacketFor > 0 {
		m.FreezeSeconds.WithLabelValues(stream).Observe(p.NoPacketFor.Seconds())
	}
}

// ObserveConnectionState increments error counters for terminal states.
func (m *Metrics) ObserveConnectionState(state webrtc.PeerConnectionState) {
	switch state {
	case webrtc.PeerConnectionStateFailed:
		m.SessionErrors.WithLabelValues("ice_failed").Inc()
	case webrtc.PeerConnectionStateClosed:
		// normal teardown, not an error
	case webrtc.PeerConnectionStateDisconnected:
		m.SessionErrors.WithLabelValues("ice_disconnected").Inc()
	}
}
