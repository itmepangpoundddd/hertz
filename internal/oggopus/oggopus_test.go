package oggopus

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func referenceCRC(page []byte) uint32 {
	var crc uint32
	for _, b := range page {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func TestChecksumMatchesBitwiseReference(t *testing.T) {
	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i*37 + 11)
	}
	if got, want := checksum(data), referenceCRC(data); got != want {
		t.Fatalf("checksum=%08x want %08x", got, want)
	}
}

type page struct {
	headerType byte
	granule    uint64
	serial     uint32
	seq        uint32
	payload    []byte
}

func parsePages(t *testing.T, raw []byte) []page {
	t.Helper()
	var pages []page
	for len(raw) > 0 {
		if len(raw) < 27 || string(raw[:4]) != "OggS" {
			t.Fatalf("bad page at offset %d", len(raw))
		}
		segments := int(raw[26])
		if len(raw) < 27+segments {
			t.Fatal("truncated segment table")
		}
		payloadLen := 0
		for _, n := range raw[27 : 27+segments] {
			payloadLen += int(n)
		}
		total := 27 + segments + payloadLen
		if len(raw) < total {
			t.Fatal("truncated page payload")
		}
		pageBytes := raw[:total]
		wantCRC := binary.LittleEndian.Uint32(pageBytes[22:26])
		zeroed := append([]byte(nil), pageBytes...)
		binary.LittleEndian.PutUint32(zeroed[22:26], 0)
		if got := checksum(zeroed); got != wantCRC {
			t.Fatalf("page CRC=%08x want %08x", got, wantCRC)
		}
		pages = append(pages, page{
			headerType: pageBytes[5],
			granule:    binary.LittleEndian.Uint64(pageBytes[6:14]),
			serial:     binary.LittleEndian.Uint32(pageBytes[14:18]),
			seq:        binary.LittleEndian.Uint32(pageBytes[18:22]),
			payload:    pageBytes[27+segments:],
		})
		raw = raw[total:]
	}
	return pages
}

func TestWriterHeadersAndPackets(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, 0x12345678)
	if err := w.WriteHeaders(); err != nil {
		t.Fatal(err)
	}
	packet := bytes.Repeat([]byte{0xfc}, 400) // CELT 20 ms, forces lacing
	if err := w.WritePacket(packet, 960); err != nil {
		t.Fatal(err)
	}
	if err := w.WritePacket([]byte{0xf8, 0x01}, 1920); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	pages := parsePages(t, buf.Bytes())
	if len(pages) != 5 {
		t.Fatalf("pages=%d want 5", len(pages))
	}
	if pages[0].headerType&0x02 == 0 || string(pages[0].payload[:8]) != "OpusHead" {
		t.Fatalf("first page is not BOS/OpusHead: %+v", pages[0])
	}
	if string(pages[1].payload[:8]) != "OpusTags" {
		t.Fatalf("second page is not OpusTags: %q", pages[1].payload[:8])
	}
	if pages[2].granule != 960 {
		t.Fatalf("first audio granule=%d want 960", pages[2].granule)
	}
	if !bytes.Equal(pages[2].payload, packet) {
		t.Fatal("audio payload changed")
	}
	if pages[3].granule != 1920 {
		t.Fatalf("second audio granule=%d want 1920", pages[3].granule)
	}
	if pages[4].headerType&0x04 == 0 {
		t.Fatal("last page is not EOS")
	}
	if pages[2].serial != 0x12345678 || pages[2].seq != 2 {
		t.Fatalf("serial/seq=%08x/%d", pages[2].serial, pages[2].seq)
	}
}

func TestOpusPacketSamples(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		want    int
	}{
		{"celt 20ms mono", []byte{0xf8}, 960},
		{"celt 10ms mono", []byte{0xf0}, 480},
		{"celt 5ms mono", []byte{0xe8}, 240},
		{"silk 20ms mono", []byte{0x08}, 960},
		{"two frames", []byte{0x09}, 1920},
		{"arbitrary three frames", []byte{0xfb, 0x03}, 2880},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := opusPacketSamples(tc.payload); got != tc.want {
				t.Fatalf("samples=%d want %d", got, tc.want)
			}
		})
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, 1)
	if err := w.WriteHeaders(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	before := buf.Len()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != before {
		t.Fatal("second Close wrote another page")
	}
}
