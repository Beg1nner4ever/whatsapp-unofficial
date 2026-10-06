package wa

import (
	"encoding/binary"
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func TestParseRecipient(t *testing.T) {
	ok := map[string]string{
		"33612345678":                "33612345678@s.whatsapp.net",
		"+33 6 12 34 56 78":          "33612345678@s.whatsapp.net",
		"120363000000000001@g.us":    "120363000000000001@g.us",
		"33612345678@s.whatsapp.net": "33612345678@s.whatsapp.net",
	}
	for in, want := range ok {
		got, err := parseRecipient(in)
		if err != nil || got.String() != want {
			t.Errorf("parseRecipient(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "123", "not a number", "1234567890123456789"} {
		if got, err := parseRecipient(bad); err == nil {
			t.Errorf("parseRecipient(%q) = %v, want error", bad, got)
		}
	}
	if j, _ := parseRecipient("33612345678"); j.Server != types.DefaultUserServer {
		t.Error("phone numbers must map to the user server")
	}
}

func TestExtractTextIncludesCaptions(t *testing.T) {
	cases := []struct {
		msg  *waE2E.Message
		want string
	}{
		{&waE2E.Message{Conversation: proto.String("hi")}, "hi"},
		{&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("link")}}, "link"},
		{&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("photo")}}, "photo"},
		{&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Caption: proto.String("doc")}}, "doc"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := extractText(c.msg); got != c.want {
			t.Errorf("extractText = %q, want %q", got, c.want)
		}
	}
}

func TestExtractMediaKeepsSenderFilenameForLaterSanitizing(t *testing.T) {
	m := extractMedia(&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
		FileName: proto.String("../../evil.plist"), URL: proto.String("https://mmg.whatsapp.net/v/t62/abc.enc?x=1"),
	}}, "ID1")
	if m.Type != "document" || m.Filename != "../../evil.plist" {
		t.Fatalf("media = %+v", m)
	}
	if got := extractMedia(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, "ID2"); got.Filename != "ID2.jpg" {
		t.Fatalf("image filename = %q", got.Filename)
	}
}

func TestDirectPathFromURL(t *testing.T) {
	if got := directPathFromURL("https://mmg.whatsapp.net/v/t62.7118-24/123_n.enc?ccb=11-4&oh=x"); got != "/v/t62.7118-24/123_n.enc" {
		t.Fatalf("got %q", got)
	}
	if got := directPathFromURL("garbage"); got != "" {
		t.Fatalf("got %q", got)
	}
}

// oggPage builds a minimal Ogg page with one segment.
func oggPage(seq uint32, granule uint64, payload []byte) []byte {
	h := make([]byte, 27)
	copy(h, "OggS")
	binary.LittleEndian.PutUint64(h[6:], granule)
	binary.LittleEndian.PutUint32(h[18:], seq)
	h[26] = 1
	return append(append(h, byte(len(payload))), payload...)
}

func TestAnalyzeOggOpus(t *testing.T) {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1                                     // version
	head[9] = 1                                     // channels
	binary.LittleEndian.PutUint16(head[10:], 312)   // pre-skip
	binary.LittleEndian.PutUint32(head[12:], 16000) // input rate: must not affect duration
	data := append(oggPage(0, 0, head), oggPage(1, 48000*7+312, []byte("audio"))...)
	data = append(data, make([]byte, 64)...) // trailing bytes so the last page is parsed

	secs, wave, err := analyzeOggOpus(data)
	if err != nil {
		t.Fatal(err)
	}
	if secs != 7 {
		t.Errorf("duration = %d, want 7", secs)
	}
	if len(wave) != 64 {
		t.Errorf("waveform length = %d", len(wave))
	}
	if _, _, err := analyzeOggOpus([]byte("not ogg")); err == nil {
		t.Error("expected error for non-Ogg data")
	}
}
