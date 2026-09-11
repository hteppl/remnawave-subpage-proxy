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

// The early rejection must never refuse something one of the alphabets decodes.
func TestDecodeAgreesWithEveryAlphabet(t *testing.T) {
	for _, s := range []string{
		"", "aGVsbG8=", "aGVsbG8", "a-_b", "a+/b", "aGVs\r\nbG8=", "aGVs bG8=",
		"text/plain; charset=utf-8", "https://t.me/support", "12", "Remnawave", "é",
	} {
		want := false
		for _, enc := range encodings {
			if _, err := enc.DecodeString(s); err == nil {
				want = true
				break
			}
		}
		if _, _, got := Decode(s, nil); got != want {
			t.Errorf("%q: Decode says %v, the alphabets say %v", s, got, want)
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
