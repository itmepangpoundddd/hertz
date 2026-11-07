package signaling

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestListenPageIsEnglishWithEmbedMode(t *testing.T) {
	page := string(listenPage)
	if !strings.Contains(page, `html lang="en"`) {
		t.Fatal("listen page is not marked as English")
	}
	if !strings.Contains(page, `.get("embed") === "1"`) {
		t.Fatal("listen page does not enable embed mode from ?embed=1")
	}
	if !strings.Contains(page, `body.embed`) {
		t.Fatal("listen page is missing compact embed styles")
	}
	for _, r := range page {
		if r >= 0x0E00 && r <= 0x0E7F {
			t.Fatalf("listen page still contains Thai text near %q", string(r))
		}
	}
}

func offerFor(t *testing.T, kind webrtc.RTPCodecType) string {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if _, err := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatal(err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	return offer.SDP
}

func TestValidateAudioOffer(t *testing.T) {
	if err := validateOffer(offerFor(t, webrtc.RTPCodecTypeAudio), 4); err != nil {
		t.Fatalf("audio offer rejected: %v", err)
	}
}

func TestValidateOfferRejectsVideo(t *testing.T) {
	if err := validateOffer(offerFor(t, webrtc.RTPCodecTypeVideo), 4); err == nil {
		t.Fatal("video offer accepted")
	}
}

func TestValidateOfferRejectsEmpty(t *testing.T) {
	if err := validateOffer("", 4); err == nil {
		t.Fatal("empty SDP accepted")
	}
}
