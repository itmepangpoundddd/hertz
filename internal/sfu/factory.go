package sfu

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/interceptor/pkg/twcc"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"

	"hertz/internal/config"
	"hertz/internal/metrics"
)

// PeerFactory builds one isolated Pion API per subscriber. A per-peer API is
// intentional: it gives each listener its own GCC bandwidth estimator and
// pacer, and prevents one estimator's state from leaking to another session.
// The expensive shared state (ICE UDP mux, setting engine) is reused.
type PeerFactory struct {
	setting   webrtc.SettingEngine
	iceServer []webrtc.ICEServer
	cfg       config.Config
	metrics   *metrics.Metrics
	log       *slog.Logger
	codec     webrtc.RTPCodecCapability
}

func NewPeerFactory(cfg config.Config, mux ice.UDPMux, m *metrics.Metrics, log *slog.Logger) (*PeerFactory, error) {
	var se webrtc.SettingEngine
	networks := []webrtc.NetworkType{webrtc.NetworkTypeUDP4}
	if strings.Contains(cfg.PublicIP, ":") {
		networks = append(networks, webrtc.NetworkTypeUDP6)
	}
	se.SetNetworkTypes(networks)
	se.SetIncludeLoopbackCandidate(cfg.IncludeLoop)
	se.SetICETimeouts(5*time.Second, 15*time.Second, 2*time.Second)
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	se.SetReceiveMTU(1500)
	se.SetSRTPReplayProtectionWindow(128)
	se.SetDTLSRetransmissionInterval(500 * time.Millisecond)
	if err := se.SetAnsweringDTLSRole(webrtc.DTLSRoleServer); err != nil {
		return nil, fmt.Errorf("set dtls role: %w", err)
	}
	if cfg.PublicIP != "" {
		se.SetNAT1To1IPs([]string{cfg.PublicIP}, webrtc.ICECandidateTypeHost)
	}
	if mux != nil {
		se.SetICEUDPMux(mux)
	}

	servers := make([]webrtc.ICEServer, 0, len(cfg.ICEServers))
	for _, s := range cfg.ICEServers {
		srv := webrtc.ICEServer{URLs: s.URLs, Username: s.Username}
		if s.Credential != "" {
			srv.Credential = s.Credential
		}
		servers = append(servers, srv)
	}

	return &PeerFactory{
		setting:   se,
		iceServer: servers,
		cfg:       cfg,
		metrics:   m,
		log:       log,
		codec:     OpusCapability(cfg.MaxBitrate),
	}, nil
}

// Codec returns the negotiated audio capability.
func (f *PeerFactory) Codec() webrtc.RTPCodecCapability { return f.codec }

// New creates a peer connection and its listener down-track.
func (f *PeerFactory) New(id, streamID, owner, remoteIP string) (*Peer, error) {
	me := &webrtc.MediaEngine{}
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: f.codec,
		PayloadType:        111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, fmt.Errorf("register opus: %w", err)
	}
	// Audio NACK is useful even at 20 ms packetization: loss bursts are
	// cheaper to repair than to conceal. Pion's default helper only enables
	// NACK for video, so register it explicitly for audio.
	me.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack"}, webrtc.RTPCodecTypeAudio)
	me.RegisterFeedback(
		webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBTransportCC},
		webrtc.RTPCodecTypeAudio,
	)
	if err := me.RegisterHeaderExtension(
		webrtc.RTPHeaderExtensionCapability{URI: sdp.TransportCCURI},
		webrtc.RTPCodecTypeAudio,
	); err != nil {
		return nil, err
	}

	ir := &interceptor.Registry{}
	var estimator cc.BandwidthEstimator
	ccFactory, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
		opts := []gcc.Option{
			gcc.SendSideBWEInitialBitrate(f.cfg.InitialBitrate),
			gcc.SendSideBWEMinBitrate(f.cfg.MinBitrate),
			gcc.SendSideBWEMaxBitrate(f.cfg.MaxBitrate),
		}
		if f.cfg.PacerMode == "leaky" {
			// A leaky bucket is useful for shaping but its queue is unbounded.
			// For live audio, a queue is worse than a dropped packet; use it
			// only when you also monitor queue growth.
			opts = append(opts, gcc.SendSideBWEPacer(gcc.NewLeakyBucketPacer(f.cfg.InitialBitrate)))
		} else {
			// Default: forward at the source's natural 20 ms pace. GCC still
			// estimates the path and drives source-side ABR.
			opts = append(opts, gcc.SendSideBWEPacer(gcc.NewNoOpPacer()))
		}
		return gcc.NewSendSideBWE(opts...)
	})
	if err != nil {
		return nil, fmt.Errorf("cc interceptor: %w", err)
	}
	ccFactory.OnNewPeerConnection(func(_ string, e cc.BandwidthEstimator) {
		estimator = e
	})
	ir.Add(ccFactory) // before NACK: enables target-bitrate-aware retransmission

	twccExt, err := twcc.NewHeaderExtensionInterceptor()
	if err != nil {
		return nil, fmt.Errorf("twcc extension: %w", err)
	}
	ir.Add(twccExt)
	if err := webrtc.ConfigureNack(me, ir); err != nil {
		return nil, fmt.Errorf("nack: %w", err)
	}
	if err := webrtc.ConfigureRTCPReports(ir); err != nil {
		return nil, fmt.Errorf("rtcp reports: %w", err)
	}
	if err := webrtc.ConfigureStatsInterceptor(ir); err != nil {
		return nil, fmt.Errorf("stats interceptor: %w", err)
	}

	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(me),
		webrtc.WithInterceptorRegistry(ir),
		webrtc.WithSettingEngine(f.setting),
	)
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: f.iceServer})
	if err != nil {
		return nil, fmt.Errorf("new peer connection: %w", err)
	}
	if estimator == nil {
		f.log.Warn("gcc estimator missing; congestion control disabled", "session", id)
	}

	dt, err := NewDownTrack(f.codec, "audio", streamID, f.cfg.QueueSize, f.cfg.SharedPayload, f.log)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("down track: %w", err)
	}
	return newPeer(id, streamID, owner, remoteIP, pc, dt, estimator, f.metrics, f.log), nil
}
