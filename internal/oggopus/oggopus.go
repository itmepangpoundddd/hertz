// Package oggopus writes a live Ogg/Opus stream. HERTZ forwards Opus packets
// without decoding them; this package only wraps those packets in an Ogg
// container so VLC and other HTTP audio clients can play the same source.
package oggopus

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	preskipSamples = 312
	sampleRate     = 48000
)

// Writer serializes Opus packets as Ogg pages. It is not safe for concurrent
// use; the HTTP handler owns one Writer per listener connection.
type Writer struct {
	w       io.Writer
	serial  uint32
	seq     uint32
	granule uint64
	lastTS  uint32
	haveTS  bool
	headers bool
	closed  bool
}

// NewWriter creates a writer for one Ogg stream. Serial must be unique among
// concurrent streams served by the same server.
func NewWriter(w io.Writer, serial uint32) *Writer {
	return &Writer{w: w, serial: serial}
}

// WriteHeaders emits the mandatory OpusHead and OpusTags pages.
func (w *Writer) WriteHeaders() error {
	if w.headers {
		return errors.New("oggopus: headers already written")
	}
	if err := w.writePacket(0x02, 0, opusHead()); err != nil {
		return err
	}
	if err := w.writePacket(0x00, 0, opusTags()); err != nil {
		return err
	}
	w.headers = true
	return nil
}

// WritePacket appends one Opus packet. The RTP timestamp is used to derive the
// granule position; HERTZ expects 48 kHz Opus with 20 ms frames, but other
// durations work as long as timestamps advance normally.
func (w *Writer) WritePacket(payload []byte, timestamp uint32) error {
	if !w.headers {
		return errors.New("oggopus: headers not written")
	}
	if len(payload) == 0 {
		return errors.New("oggopus: empty Opus packet")
	}
	samples := opusPacketSamples(payload)
	if w.haveTS {
		delta := timestamp - w.lastTS
		if delta > 0 && delta < sampleRate*10 {
			samples = int(delta)
		}
	}
	w.lastTS = timestamp
	w.haveTS = true
	w.granule += uint64(samples)
	return w.writePacket(0x00, w.granule, payload)
}

// Close writes an end-of-stream page. It is safe to call more than once.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.writeEmptyPage(0x04, w.granule)
}

func (w *Writer) writePacket(headerType byte, granule uint64, packet []byte) error {
	if len(packet) == 0 {
		return errors.New("oggopus: empty page packet")
	}
	segments := make([]byte, 0, len(packet)/255+1)
	remaining := len(packet)
	for remaining >= 255 {
		segments = append(segments, 255)
		remaining -= 255
	}
	segments = append(segments, byte(remaining))
	return w.writePage(headerType, granule, segments, packet)
}

func (w *Writer) writeEmptyPage(headerType byte, granule uint64) error {
	return w.writePage(headerType, granule, nil, nil)
}

func (w *Writer) writePage(headerType byte, granule uint64, segments, payload []byte) error {
	page := make([]byte, 27+len(segments)+len(payload))
	copy(page[0:4], "OggS")
	page[4] = 0 // version
	page[5] = headerType
	binary.LittleEndian.PutUint64(page[6:14], granule)
	binary.LittleEndian.PutUint32(page[14:18], w.serial)
	binary.LittleEndian.PutUint32(page[18:22], w.seq)
	page[26] = byte(len(segments))
	copy(page[27:27+len(segments)], segments)
	copy(page[27+len(segments):], payload)
	binary.LittleEndian.PutUint32(page[22:26], checksum(page))
	if _, err := w.w.Write(page); err != nil {
		return err
	}
	w.seq++
	return nil
}

func opusHead() []byte {
	b := make([]byte, 19)
	copy(b[0:8], "OpusHead")
	b[8] = 1 // version
	b[9] = 2 // stereo
	binary.LittleEndian.PutUint16(b[10:12], preskipSamples)
	binary.LittleEndian.PutUint32(b[12:16], sampleRate)
	binary.LittleEndian.PutUint16(b[16:18], 0) // output gain
	b[18] = 0                                  // channel mapping family
	return b
}

func opusTags() []byte {
	vendor := []byte("HERTZ")
	comment := []byte("ENCODER=HERTZ")
	b := make([]byte, 0, 8+4+len(vendor)+4+4+len(comment))
	b = append(b, "OpusTags"...)
	b = appendLE32(b, uint32(len(vendor)))
	b = append(b, vendor...)
	b = appendLE32(b, 1)
	b = appendLE32(b, uint32(len(comment)))
	b = append(b, comment...)
	return b
}

func appendLE32(b []byte, v uint32) []byte {
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], v)
	return append(b, raw[:]...)
}

// opusPacketSamples returns the number of 48 kHz samples in one Opus packet.
// It reads the TOC byte and frame-count code from RFC 6716.
func opusPacketSamples(payload []byte) int {
	if len(payload) == 0 {
		return 0
	}
	toc := payload[0]
	config := toc >> 3
	var frame int
	switch {
	case config < 12: // SILK
		frame = []int{10, 20, 40, 60}[config&3] * 48
	case config < 16: // Hybrid
		frame = []int{10, 20}[config&1] * 48
	default: // CELT
		switch (config - 16) & 3 {
		case 0:
			frame = 120 // 2.5 ms
		case 1:
			frame = 240 // 5 ms
		case 2:
			frame = 480 // 10 ms
		default:
			frame = 960 // 20 ms
		}
	}
	switch toc & 3 {
	case 0:
		return frame
	case 1, 2:
		return frame * 2
	default:
		if len(payload) < 2 {
			return frame
		}
		return frame * int(payload[1]&0x3f)
	}
}

var crcTable = func() [256]uint32 {
	var t [256]uint32
	for i := range t {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return t
}()

func checksum(page []byte) uint32 {
	var crc uint32
	for _, b := range page {
		crc = (crc << 8) ^ crcTable[byte(crc>>24)^b]
	}
	return crc
}
