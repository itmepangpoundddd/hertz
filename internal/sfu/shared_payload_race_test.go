package sfu

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"

	"hertz/internal/config"
	"hertz/internal/metrics"
)

// TestSharedPayloadRace is an opt-in integration test. It negotiates two real
// PeerConnections with TWCC/NACK/SRTP and writes the same payload to both
// DownTracks concurrently. Run with:
//
//	HERTZ_RACE_SHARED_PAYLOAD=1 CGO_ENABLED=1 go test -race ./internal/sfu \
//	  -run TestSharedPayloadRace -count=1
func TestSharedPayloadRace(t *testing.T) {
	if os.Getenv("HERTZ_RACE_SHARED_PAYLOAD") != "1" {
		t.Skip("set HERTZ_RACE_SHARED_PAYLOAD=1 to run the full Pion race test")
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := metrics.New()
	cfg := config.Config{
		QueueSize:           10,
		InitialBitrate:      256_000,
		MinBitrate:          96_000,
		MaxBitrate:          512_000,
		PacerMode:           "noop",
		StatsInterval:       5 * time.Second,
		IncludeLoop:         true,
		SharedPayload:       true,
		MaxSessions:         10,
		JoinRatePerSecond:   100,
		JoinBurst:           10,
		IPJoinRatePerSecond: 100,
		IPJoinBurst:         10,
	}
	factory, err := NewPeerFactory(cfg, nil, m, log)
	if err != nil {
		t.Fatal(err)
	}

	clients := make([]*webrtc.PeerConnection, 0, 2)
	peers := make([]*Peer, 0, 2)
	for i := 0; i < 2; i++ {
		peer, client, err := connectTestPeer(t, factory, i)
		if err != nil {
			t.Fatalf("peer %d: %v", i, err)
		}
		peers = append(peers, peer)
		clients = append(clients, client)
	}
	defer func() {
		for _, p := range peers {
			p.Close()
		}
		for _, c := range clients {
			_ = c.Close()
		}
	}()

	// One shared payload per packet; every DownTrack points at these bytes.
	payload := make([]byte, 960)
	payload[0] = 0xf8
	for i := 0; i < 120; i++ {
		pkt := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				SequenceNumber: uint16(100 + i),
				Timestamp:      uint32(i) * 960,
				SSRC:           0x11223344,
				PayloadType:    111,
			},
			Payload: payload,
		}
		var wg sync.WaitGroup
		for _, p := range peers {
			wg.Add(1)
			go func(p *Peer) {
				defer wg.Done()
				p.Track().WriteRTP(pkt)
			}(p)
		}
		wg.Wait()
	}
	time.Sleep(300 * time.Millisecond)
}

func connectTestPeer(t *testing.T, factory *PeerFactory, idx int) (*Peer, *webrtc.PeerConnection, error) {
	t.Helper()
	clientAPI, err := testClientAPI()
	if err != nil {
		return nil, nil, err
	}
	client, err := clientAPI.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, nil, err
	}
	client.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go func() {
			for {
				if _, _, err := track.ReadRTP(); err != nil {
					return
				}
			}
		}()
	})
	if _, err := client.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		return nil, nil, err
	}
	offer, err := client.CreateOffer(nil)
	if err != nil {
		return nil, nil, err
	}
	gather := webrtc.GatheringCompletePromise(client)
	if err := client.SetLocalDescription(offer); err != nil {
		return nil, nil, err
	}
	<-gather

	peer, err := factory.New("race-"+string(rune('a'+idx)), "main", "race", "127.0.0.1")
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	answer, err := peer.Answer(ctx, *client.LocalDescription())
	if err != nil {
		return nil, nil, err
	}
	if err := client.SetRemoteDescription(answer); err != nil {
		return nil, nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if peer.Track().ready.Load() && client.ConnectionState() == webrtc.PeerConnectionStateConnected {
			return peer, client, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, nil, context.DeadlineExceeded
}

func testClientAPI() (*webrtc.API, error) {
	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	me.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack"}, webrtc.RTPCodecTypeAudio)
	if err := me.RegisterHeaderExtension(
		webrtc.RTPHeaderExtensionCapability{URI: sdp.TransportCCURI},
		webrtc.RTPCodecTypeAudio,
	); err != nil {
		return nil, err
	}
	ir := &interceptor.Registry{}
	if err := webrtc.ConfigureNack(me, ir); err != nil {
		return nil, err
	}
	if err := webrtc.ConfigureRTCPReports(ir); err != nil {
		return nil, err
	}
	if err := webrtc.ConfigureTWCCSender(me, ir); err != nil {
		return nil, err
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithInterceptorRegistry(ir)), nil
}
