//go:build !windows

package rtpengine

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnixIngestCreatesPrivateSocket(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "private", "ingest.sock")
	h := NewHandler(HandlerConfig{
		StreamID:      "test",
		PayloadType:   111,
		MaxPacketSize: 1500,
		JitterWindow:  10 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	u, err := NewUnixIngest(path, h, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode=%#o want 0600", got)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Fatalf("socket dir mode=%#o want 0700", got)
	}
}
