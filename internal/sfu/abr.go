package sfu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"hertz/internal/config"
	"hertz/internal/metrics"
)

// ABRController implements broadcast-oriented source adaptation.
//
// An SFU cannot lower Opus bitrate without transcoding. Instead, HERTZ
// measures every listener's GCC target and asks the source encoder for the
// median bitrate. This keeps the broadcast useful for the majority; individual
// listeners on worse paths are protected by GCC pacing, NACK, and Opus PLC.
// If strict per-listener quality tiers are required, add transcoded renditions
// and switch the listener's track instead of lowering the source.
type ABRController struct {
	cfg     config.Config
	metrics *metrics.Metrics
	log     *slog.Logger
	client  *http.Client

	current map[string]int
	pending map[string]abrPending
}

type abrPending struct {
	dir int // +1 increase, -1 decrease
}

func NewABRController(cfg config.Config, m *metrics.Metrics, log *slog.Logger) *ABRController {
	return &ABRController{
		cfg:     cfg,
		metrics: m,
		log:     log,
		client:  &http.Client{Timeout: 3 * time.Second},
		current: make(map[string]int),
		pending: make(map[string]abrPending),
	}
}

// Run samples every 5 s (or the stats interval if larger) and applies a
// debounce so a single transient estimate does not yank the encoder.
func (a *ABRController) Run(ctx context.Context, mgr *Manager) {
	interval := a.cfg.StatsInterval * 2
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.step(mgr)
		}
	}
}

func (a *ABRController) step(mgr *Manager) {
	byStream := make(map[string][]int)
	for _, p := range mgr.Snapshot() {
		// Ignore sessions that have not completed a stats sample yet and
		// listeners that are clearly gone.
		target := p.TargetBitrate()
		if target > 0 {
			byStream[p.StreamID] = append(byStream[p.StreamID], target)
		}
	}
	a.applyTargets(byStream)
}

// applyTargets is separated from Manager so the ABR policy can be unit tested
// with synthetic listener samples.
func (a *ABRController) applyTargets(byStream map[string][]int) {
	for stream, targets := range byStream {
		target := clamp(median(targets), a.cfg.MinBitrate, a.cfg.MaxBitrate)
		prev, seen := a.current[stream]
		if !seen {
			// The encoder must learn the initial target too; otherwise the
			// first change would be relative to an unknown source bitrate.
			if err := a.notify(stream, target); err != nil {
				a.log.Warn("encoder control webhook failed", "stream", stream, "err", err)
				continue
			}
			a.current[stream] = target
			a.metrics.SourceBitrate.WithLabelValues(stream).Set(float64(target))
			a.log.Info("source bitrate target initialized", "stream", stream, "bps", target)
			continue
		}

		deltaPct := abs(target-prev) * 100 / max(prev, 1)
		if deltaPct <= 8 {
			delete(a.pending, stream)
			continue
		}

		dir := 1
		if target < prev {
			dir = -1
		}
		pending, ok := a.pending[stream]
		if !ok || pending.dir != dir {
			// First significant move in this direction; wait one more round.
			a.pending[stream] = abrPending{dir: dir}
			continue
		}
		// Two consecutive >8% moves in the same direction. The magnitudes may
		// differ; natural median drift is not exactly equal.
		if err := a.notify(stream, target); err != nil {
			a.log.Warn("encoder control webhook failed", "stream", stream, "err", err)
			continue // retry on the next tick
		}
		a.current[stream] = target
		a.metrics.SourceBitrate.WithLabelValues(stream).Set(float64(target))
		a.log.Info("source bitrate target changed", "stream", stream, "from", prev, "to", target)
		delete(a.pending, stream)
	}
}

func (a *ABRController) notify(stream string, bps int) error {
	if a.cfg.EncoderControlURL == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"stream_id":   stream,
		"bitrate_bps": bps,
	})
	req, err := http.NewRequest(http.MethodPost, a.cfg.EncoderControlURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.EncoderControlToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.EncoderControlToken)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %s", resp.Status)
	}
	return nil
}

func median(v []int) int {
	if len(v) == 0 {
		return 0
	}
	cp := append([]int(nil), v...)
	sort.Ints(cp)
	return cp[len(cp)/2]
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
