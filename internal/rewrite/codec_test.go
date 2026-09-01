package rewrite

import (
	"encoding/base64"
	"testing"
)

func TestDecodeBase64(t *testing.T) {
	announce := "Использовано {TRAFFIC_USED} из {TRAFFIC_AVAILABLE}"
	encoded := base64.StdEncoding.EncodeToString([]byte(announce))

	t.Run("prefixed", func(t *testing.T) {
		text, form, ok := DecodeBase64(Base64Prefix + encoded)
		if !ok || text != announce || form != FormBase64Prefixed {
			t.Fatalf("got (%q, %v, %v)", text, form, ok)
		}
	})

	t.Run("bare", func(t *testing.T) {
		text, form, ok := DecodeBase64(encoded)
		if !ok || text != announce || form != FormBase64 {
			t.Fatalf("got (%q, %v, %v)", text, form, ok)
		}
	})

	t.Run("plain text is not mistaken for base64", func(t *testing.T) {
		// Many plain words are syntactically valid base64.
		for _, input := range []string{"Welcome!", "Hello, world", "10.50 GB", "abc"} {
			if _, _, ok := DecodeBase64(input); ok {
				t.Errorf("DecodeBase64(%q) unexpectedly decoded", input)
			}
		}
	})

	t.Run("binary payloads are rejected", func(t *testing.T) {
		binary := base64.StdEncoding.EncodeToString([]byte{0x00, 0x01, 0x02, 0x03, 0xff})
		if _, _, ok := DecodeBase64(binary); ok {
			t.Error("binary base64 should not be treated as text")
		}
	})
}

func TestEncodeRoundTrip(t *testing.T) {
	text := "Осталось 42 ГБ"
	for _, form := range []Form{FormPlain, FormBase64, FormBase64Prefixed} {
		encoded := Encode(text, form)
		if form == FormPlain {
			if encoded != text {
				t.Errorf("plain form changed the value: %q", encoded)
			}
			continue
		}
		decoded, gotForm, ok := DecodeBase64(encoded)
		if !ok || decoded != text || gotForm != form {
			t.Errorf("round trip for form %v failed: (%q, %v, %v)", form, decoded, gotForm, ok)
		}
	}
}
