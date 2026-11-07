package sfu

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"hertz/internal/config"
	"hertz/internal/metrics"
)

func TestABRTwoConsecutiveDirectionalChanges(t *testing.T) {
	var got []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Bitrate int `json:"bitrate_bps"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		got = append(got, body.Bitrate)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	a := NewABRController(config.Config{
		MinBitrate:        64_000,
		MaxBitrate:        320_000,
		EncoderControlURL: srv.URL,
	}, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	apply := func(bps int) { a.applyTargets(map[string][]int{"main": {bps}}) }

	apply(200_000) // initial value must be sent
	apply(205_000) // +2.5%, inside hysteresis
	apply(224_000) // +12%, first significant move
	apply(230_000) // +15%, same direction -> apply
	apply(220_000) // -4.3%, resets pending
	apply(200_000) // -13%, first significant down move
	apply(196_000) // -15%, same direction -> apply

	want := []int{200_000, 230_000, 196_000}
	if len(got) != len(want) {
		t.Fatalf("webhook calls=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("webhook calls=%v want %v", got, want)
		}
	}
}

func TestABROppositeDirectionResetsPending(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	a := NewABRController(config.Config{
		MinBitrate:        64_000,
		MaxBitrate:        320_000,
		EncoderControlURL: srv.URL,
	}, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	apply := func(bps int) { a.applyTargets(map[string][]int{"main": {bps}}) }

	apply(200_000)
	apply(240_000) // up, pending
	apply(180_000) // down, resets pending
	if calls != 1 {
		t.Fatalf("calls=%d want 1 (only initial)", calls)
	}
	apply(160_000) // second down, applies
	if calls != 2 {
		t.Fatalf("calls=%d want 2", calls)
	}
}
