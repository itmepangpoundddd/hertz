// Command loadgen creates N headless Pion subscribers and measures join
// success, time-to-first-RTP, received bitrate, loss, jitter, and reordering.
// It is deliberately small so it can run beside the SFU or on many machines.
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"hertz/internal/auth"
)

type subscribeRequest struct {
	StreamID string `json:"stream_id"`
	SDP      string `json:"sdp"`
}

type subscribeResponse struct {
	SessionID string                    `json:"session_id"`
	StreamID  string                    `json:"stream_id"`
	Answer    webrtc.SessionDescription `json:"answer"`
}

type listenerStats struct {
	packets atomic.Uint64
	bytes   atomic.Uint64
	lost    atomic.Uint64
	dup     atomic.Uint64
	ooo     atomic.Uint64
}

type listener struct {
	id    string
	pc    *webrtc.PeerConnection
	res   *result
	start time.Time
	stats listenerStats

	firstMu     sync.Mutex
	firstAt     time.Time
	haveSeq     bool
	lastSeq     uint16
	jitter      float64
	prevTransit float64
}

type result struct {
	attempted atomic.Uint64
	joined    atomic.Uint64
	failed    atomic.Uint64
	sessions  atomic.Int64

	firstMu     sync.Mutex
	firstDelays []time.Duration
}

var listeners sync.Map // map[string]*listener

func main() {
	var (
		baseURL   = flag.String("url", "http://127.0.0.1:8080", "HERTZ signaling base URL")
		token     = flag.String("token", "", "subscriber token (all listeners share it)")
		secret    = flag.String("secret", "", "HMAC secret; issue a unique token per listener")
		stream    = flag.String("stream", "main", "stream id")
		n         = flag.Int("listeners", 100, "number of listeners")
		ramp      = flag.Int("ramp", 20, "listeners started per second")
		duration  = flag.Duration("duration", 60*time.Second, "test duration after join")
		joinTO    = flag.Duration("join-timeout", 20*time.Second, "per-listener join timeout")
		insecure  = flag.Bool("insecure", false, "skip TLS verification")
		subPrefix = flag.String("sub-prefix", "load", "subject prefix when -secret is used")
	)
	flag.Parse()
	if *n <= 0 {
		fmt.Fprintln(os.Stderr, "listeners must be > 0")
		os.Exit(2)
	}
	if *ramp <= 0 {
		*ramp = 1
	}
	if *token == "" && *secret == "" {
		fmt.Fprintln(os.Stderr, "warning: no token/secret; sending token 'dev' (server must allow insecure dev auth)")
		*token = "dev"
	}
	var signer *auth.Signer
	if *secret != "" {
		var err error
		signer, err = auth.NewSigner(*secret, false)
		if err != nil {
			fmt.Fprintln(os.Stderr, "signer:", err)
			os.Exit(2)
		}
	}

	tr := &http.Transport{
		MaxIdleConns:        *n + 16,
		MaxIdleConnsPerHost: *n + 16,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: *insecure}, //nolint:gosec // explicit flag
	}
	client := &http.Client{Transport: tr, Timeout: *joinTO + 10*time.Second}
	res := &result{}
	base := strings.TrimRight(*baseURL, "/")
	api, err := newClientAPI()
	if err != nil {
		fmt.Fprintln(os.Stderr, "webrtc api:", err)
		os.Exit(2)
	}

	startAll := time.Now()
	var wg sync.WaitGroup
	gate := time.NewTicker(time.Second / time.Duration(*ramp))
	defer gate.Stop()
	for i := 0; i < *n; i++ {
		<-gate.C
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			l := &listener{
				id:    fmt.Sprintf("%s-%d", *subPrefix, idx),
				res:   res,
				start: time.Now(),
			}
			res.attempted.Add(1)
			listeners.Store(l.id, l)
			if err := runListener(client, api, base, *stream, *token, signer, l, *duration); err != nil {
				res.failed.Add(1)
				fmt.Fprintf(os.Stderr, "listener %s: %v\n", l.id, err)
			}
		}(i)
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var prevPackets, prevBytes uint64
	for now := range ticker.C {
		pkts, bytes, lost, dup, ooo := aggregate()
		dPkts := pkts - prevPackets
		dBytes := bytes - prevBytes
		prevPackets, prevBytes = pkts, bytes
		lossRatio := 0.0
		if pkts+lost > 0 {
			lossRatio = float64(lost) / float64(pkts+lost)
		}
		line, _ := json.Marshal(map[string]any{
			"t":          time.Since(startAll).Round(time.Second).String(),
			"attempted":  res.attempted.Load(),
			"joined":     res.joined.Load(),
			"failed":     res.failed.Load(),
			"active":     res.sessions.Load(),
			"pps":        dPkts,
			"rx_bps":     int64(dBytes * 8),
			"total_pkts": pkts,
			"loss_ratio": math.Round(lossRatio*1e6) / 1e6,
			"duplicates": dup,
			"reordered":  ooo,
		})
		fmt.Println(string(line))
		_ = now
		if res.attempted.Load() == uint64(*n) && res.failed.Load()+res.joined.Load() == uint64(*n) && res.sessions.Load() == 0 {
			break
		}
	}

	wg.Wait()
	pkts, bytes, lost, dup, ooo := aggregate()
	res.firstMu.Lock()
	delays := append([]time.Duration(nil), res.firstDelays...)
	res.firstMu.Unlock()
	sort.Slice(delays, func(i, j int) bool { return delays[i] < delays[j] })
	lossRatio := 0.0
	if pkts+lost > 0 {
		lossRatio = float64(lost) / float64(pkts+lost)
	}
	summary, _ := json.Marshal(map[string]any{
		"attempted":           res.attempted.Load(),
		"joined":              res.joined.Load(),
		"failed":              res.failed.Load(),
		"join_success":        float64(res.joined.Load()) / math.Max(1, float64(res.attempted.Load())),
		"first_packet_p50_ms": percentile(delays, 0.50).Milliseconds(),
		"first_packet_p95_ms": percentile(delays, 0.95).Milliseconds(),
		"first_packet_p99_ms": percentile(delays, 0.99).Milliseconds(),
		"packets":             pkts,
		"bytes":               bytes,
		"loss_ratio":          math.Round(lossRatio*1e6) / 1e6,
		"duplicates":          dup,
		"reordered":           ooo,
		"duration":            time.Since(startAll).Round(time.Second).String(),
	})
	fmt.Println("SUMMARY " + string(summary))
}

func newClientAPI() (*webrtc.API, error) {
	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	// Advertise transport-cc so the SFU's GCC has real feedback, and generate
	// TWCC reports as a browser would. ConfigureTWCCSender registers both the
	// feedback line and the header extension.
	me.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack"}, webrtc.RTPCodecTypeAudio)
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
	return webrtc.NewAPI(
		webrtc.WithMediaEngine(me),
		webrtc.WithInterceptorRegistry(ir),
	), nil
}

func runListener(client *http.Client, api *webrtc.API, base, stream, token string, signer *auth.Signer, l *listener, duration time.Duration) error {
	if signer != nil {
		tok, err := signer.Sign(auth.Claims{
			Subject:   l.id,
			Stream:    stream,
			Kind:      auth.KindSubscribe,
			ExpiresAt: time.Now().Add(30 * time.Minute).Unix(),
		})
		if err != nil {
			return err
		}
		token = tok
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	l.pc = pc
	defer pc.Close() //nolint:errcheck

	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			l.onPacket(pkt)
		}
	})

	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		return err
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return err
	}
	<-gatherDone

	local := pc.LocalDescription()
	if local == nil {
		return fmt.Errorf("nil local description")
	}
	body, _ := json.Marshal(subscribeRequest{StreamID: stream, SDP: local.SDP})
	req, err := http.NewRequest(http.MethodPost, base+"/v1/subscribe", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("subscribe status %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var sr subscribeResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return err
	}
	if err := pc.SetRemoteDescription(sr.Answer); err != nil {
		return err
	}

	l.res.joined.Add(1)
	l.res.sessions.Add(1)
	defer l.res.sessions.Add(-1)
	defer deleteSession(client, base, sr.SessionID, token)

	time.Sleep(duration)
	return nil
}

func deleteSession(client *http.Client, base, id, token string) {
	req, err := http.NewRequest(http.MethodDelete, base+"/v1/sessions/"+id, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func (l *listener) onPacket(pkt *rtp.Packet) {
	now := time.Now()
	l.firstMu.Lock()
	if l.firstAt.IsZero() {
		l.firstAt = now
		l.res.firstMu.Lock()
		l.res.firstDelays = append(l.res.firstDelays, now.Sub(l.start))
		l.res.firstMu.Unlock()
	}
	l.firstMu.Unlock()

	if l.haveSeq {
		delta := int16(pkt.SequenceNumber - l.lastSeq)
		switch {
		case delta == 1:
		case delta > 1:
			l.stats.lost.Add(uint64(delta - 1))
		case delta == 0:
			l.stats.dup.Add(1)
		default:
			l.stats.ooo.Add(1)
		}
		transit := float64(now.UnixNano())/1e9 - float64(pkt.Timestamp)/48000.0
		if l.prevTransit != 0 {
			d := math.Abs(transit - l.prevTransit)
			l.jitter += (d - l.jitter) / 16
		}
		l.prevTransit = transit
	}
	l.haveSeq = true
	l.lastSeq = pkt.SequenceNumber
	l.stats.packets.Add(1)
	l.stats.bytes.Add(uint64(len(pkt.Payload) + 12))
}

func aggregate() (packets, bytes, lost, dup, ooo uint64) {
	listeners.Range(func(_, v any) bool {
		l := v.(*listener)
		packets += l.stats.packets.Load()
		bytes += l.stats.bytes.Load()
		lost += l.stats.lost.Load()
		dup += l.stats.dup.Load()
		ooo += l.stats.ooo.Load()
		return true
	})
	return
}

func percentile(v []time.Duration, p float64) time.Duration {
	if len(v) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(v)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(v) {
		idx = len(v) - 1
	}
	return v[idx]
}
