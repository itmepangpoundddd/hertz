package rtpengine

import (
	"os"
	"runtime"
	"testing"

	"github.com/pion/rtp"
)

var cloneSink *rtp.Packet

func benchPacket() *rtp.Packet {
	return &rtp.Packet{
		Header: rtp.Header{
			Version:          2,
			SequenceNumber:   1234,
			Timestamp:        56789,
			SSRC:             0x11223344,
			PayloadType:      111,
			Extension:        true,
			ExtensionProfile: rtp.ExtensionProfileOneByte,
		},
		Payload: make([]byte, 960),
	}
}

func BenchmarkClonePacket(b *testing.B) {
	p := benchPacket()
	_ = p.Header.SetExtension(3, []byte{1, 2, 3})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ClonePacket(p)
	}
}

func BenchmarkClonePacketSharedPayload(b *testing.B) {
	p := benchPacket()
	_ = p.Header.SetExtension(3, []byte{1, 2, 3})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ClonePacketSharedPayload(p)
	}
}

// TestCloneGCPause measures the GC pause and allocation volume of the two
// fanout paths under a fixed clone count. It is opt-in because it allocates
// hundreds of MB in the copy case.
//
// HERTZ_GC_MEASURE=1 go test ./internal/rtpengine -run TestCloneGCPause -v
func TestCloneGCPause(t *testing.T) {
	if os.Getenv("HERTZ_GC_MEASURE") != "1" {
		t.Skip("set HERTZ_GC_MEASURE=1 to run the allocation/GC comparison")
	}
	p := benchPacket()
	_ = p.Header.SetExtension(3, []byte{1, 2, 3})
	const n = 500_000

	measure := func(name string, clone func(*rtp.Packet) *rtp.Packet) {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for i := 0; i < n; i++ {
			cloneSink = clone(p)
		}
		runtime.ReadMemStats(&after)
		t.Logf("%s: alloc=%d MB, GCs=%d, pause=%d ms",
			name,
			(after.TotalAlloc-before.TotalAlloc)/(1<<20),
			after.NumGC-before.NumGC,
			(after.PauseTotalNs-before.PauseTotalNs)/1_000_000,
		)
	}
	measure("copy", ClonePacket)
	measure("shared", ClonePacketSharedPayload)
}
