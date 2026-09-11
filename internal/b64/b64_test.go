package b64

import (
	"encoding/base64"
	"testing"
)

func TestDecodeFindsTheAlphabet(t *testing.T) {
	const text = "subject?>>"
	for _, enc := range encodings {
		got, used, ok := Decode(" "+enc.EncodeToString([]byte(text))+"\n", nil)
		if !ok || string(got) != text {
			t.Errorf("%v: got %q, %v", enc, got, ok)
		}
		if used.EncodeToString(got) != enc.EncodeToString([]byte(text)) {
			t.Errorf("%v: reported an encoding that does not round-trip", enc)
		}
	}
}

func TestDecodeHonoursAccept(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("hello"))
	if _, _, ok := Decode(encoded, func([]byte) bool { return false }); ok {
		t.Error("a rejected result must not be returned")
	}
	if _, _, ok := Decode("not base64 !", nil); ok {
		t.Error("invalid input decoded")
	}
}
