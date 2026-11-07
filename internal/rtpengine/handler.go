package rtpengine

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtp"
	"golang.org/x/time/rate"
)

// HandlerConfig controls RTP validation for one logical stream.
type HandlerConfig struct {
	StreamID      string
	PayloadType   uint8
	ExpectedSSRC  uint32
	MaxPacketSize int
	MaxPPS        int
	JitterWindow  time.Duration
	JitterTick    time.Duration
}

// PacketHandler validates source RTP, feeds one jitter buffer, and exposes a
// snapshot for Prometheus. UDP and Unix-socket ingest share this handler.
type PacketHandler struct {
	cfg HandlerConfig
	log *slog.Logger
	jb  *JitterBuffer

	limiter *rate.Limiter

	mu         sync.Mutex
	ssrc       uint32
	source     string
	lastPacket time.Time

	packets   atomic.Uint64
	bytes     atomic.Uint64
	invalid   atomic.Uint64
	badRTP    atomic.Uint64
	badPT     atomic.Uint64
	badSSRC   atomic.Uint64
	badSource atomic.Uint64
	rateDrop  atomic.Uint64

	lastSeen atomic.Int64
	done     chan struct{}
	stopOnce sync.Once
}

// NewHandler builds a handler. The caller must call Start to begin playout.
func NewHandler(cfg HandlerConfig, log *slog.Logger) *PacketHandler {
	if cfg.MaxPacketSize <= 0 {
		cfg.MaxPacketSize = 1500
	}
	if cfg.JitterTick <= 0 {
		cfg.JitterTick = 5 * time.Millisecond
	}
	if cfg.MaxPPS <= 0 {
		cfg.MaxPPS = 500
	}
	if log == nil {
		log = slog.Default()
	}
	return &PacketHandler{
		cfg:     cfg,
		log:     log,
		jb:      NewJitterBuffer(cfg.JitterWindow, 48000),
		limiter: rate.NewLimiter(rate.Limit(cfg.MaxPPS), max(1, cfg.MaxPPS/10)),
		done:    make(chan struct{}),
	}
}

// Start launches the jitter-buffer playout goroutine. emit is called serially
// from that goroutine and must not block for long.
func (h *PacketHandler) Start(emit func(*rtp.Packet)) {
	go h.jb.Run(h.done, h.cfg.JitterTick, emit)
}

// Close stops playout. It is safe to call more than once.
func (h *PacketHandler) Close() {
	h.stopOnce.Do(func() { close(h.done) })
}

// Handle validates one raw RTP datagram. raw may be reused by the caller as
// soon as Handle returns; all retained data is copied. source is the UDP
// IP:port (or "unix"). The first accepted source is pinned so a different
// sender cannot poison sequence numbers.
func (h *PacketHandler) Handle(raw []byte, at time.Time, source string) bool {
	if len(raw) == 0 || len(raw) > h.cfg.MaxPacketSize {
		h.invalid.Add(1)
		h.badRTP.Add(1)
		return false
	}
	if !h.limiter.Allow() {
		h.invalid.Add(1)
		h.rateDrop.Add(1)
		return false
	}

	pkt := &rtp.Packet{}
	if err := pkt.Unmarshal(raw); err != nil || pkt.Version != 2 || len(pkt.Payload) == 0 {
		h.invalid.Add(1)
		h.badRTP.Add(1)
		return false
	}
	if h.cfg.PayloadType != 0 && pkt.PayloadType != h.cfg.PayloadType {
		h.invalid.Add(1)
		h.badPT.Add(1)
		return false
	}

	ok, reset, sourceMismatch := h.acceptSource(pkt.SSRC, source, at)
	if !ok {
		h.invalid.Add(1)
		if sourceMismatch {
			h.badSource.Add(1)
		} else {
			h.badSSRC.Add(1)
		}
		return false
	}
	if reset {
		h.jb.Reset(pkt.SequenceNumber, at)
		h.log.Warn("source SSRC changed; resetting jitter buffer",
			"stream", h.cfg.StreamID, "new_ssrc", pkt.SSRC)
	}

	// The source is untrusted. Strip extensions it may have added; HERTZ
	// re-adds only the transport-cc extension for each subscriber.
	StripExtensions(pkt)
	owned := ClonePacket(pkt)
	if !h.jb.Push(owned, at) {
		return false // duplicate or late; already counted by the buffer
	}

	h.packets.Add(1)
	h.bytes.Add(uint64(len(raw)))
	h.lastSeen.Store(at.UnixNano())
	return true
}

// acceptSource pins both SSRC and UDP source. A restart may change either
// after 2 s of silence; until then a mismatch is an injection attempt.
func (h *PacketHandler) acceptSource(ssrc uint32, source string, at time.Time) (ok, reset, sourceMismatch bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.cfg.ExpectedSSRC != 0 && ssrc != h.cfg.ExpectedSSRC {
		return false, false, false
	}
	if h.ssrc == 0 {
		h.ssrc = ssrc
		h.source = source
		h.lastPacket = at
		return true, false, false
	}

	sameSSRC := ssrc == h.ssrc
	sameSource := source == "" || h.source == "" || source == h.source
	if sameSSRC && sameSource {
		h.lastPacket = at
		return true, false, false
	}
	// A different IP:port is only acceptable when the SSRC also changes after
	// silence. Otherwise it is sequence poisoning from a new source.
	if !sameSource && sameSSRC {
		return false, false, true
	}
	if !h.lastPacket.IsZero() && at.Sub(h.lastPacket) > 2*time.Second {
		h.ssrc = ssrc
		h.source = source
		h.lastPacket = at
		return true, true, false
	}
	return false, false, !sameSource
}

// RejectSource counts a datagram rejected by the ingest IP allowlist.
func (h *PacketHandler) RejectSource() {
	h.invalid.Add(1)
	h.badSource.Add(1)
}

// Snapshot is a consistent point-in-time view for metrics.
type Snapshot struct {
	StreamID    string
	SSRC        uint32
	Source      string
	Packets     uint64
	Bytes       uint64
	Invalid     uint64
	BadRTP      uint64
	BadPT       uint64
	BadSSRC     uint64
	BadSource   uint64
	RateDropped uint64
	Jitter      Stats
	LastPacket  time.Time
}

// Snapshot returns the current ingest counters.
func (h *PacketHandler) Snapshot() Snapshot {
	h.mu.Lock()
	ssrc := h.ssrc
	source := h.source
	h.mu.Unlock()

	var last time.Time
	if ns := h.lastSeen.Load(); ns != 0 {
		last = time.Unix(0, ns)
	}
	return Snapshot{
		StreamID:    h.cfg.StreamID,
		SSRC:        ssrc,
		Source:      source,
		Packets:     h.packets.Load(),
		Bytes:       h.bytes.Load(),
		Invalid:     h.invalid.Load(),
		BadRTP:      h.badRTP.Load(),
		BadPT:       h.badPT.Load(),
		BadSSRC:     h.badSSRC.Load(),
		BadSource:   h.badSource.Load(),
		RateDropped: h.rateDrop.Load(),
		Jitter:      h.jb.Stats(),
		LastPacket:  last,
	}
}
