package b64

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"
)

// referenceDecode is the implementation Decode replaced: every alphabet in
// turn, each with its own allocation, no early rejection.
func referenceDecode(s string, accept func([]byte) bool) ([]byte, *base64.Encoding, bool) {
	s = strings.TrimSpace(s)
	for _, enc := range encodings {
		raw, err := enc.DecodeString(s)
		if err != nil {
			continue
		}
		if accept == nil || accept(raw) {
			return raw, enc, true
		}
	}
	return nil, nil, false
}

func FuzzDecodeMatchesReference(f *testing.F) {
	for _, seed := range []string{
		"", " ", "aGVsbG8=", "aGVsbG8", "aGVs\r\nbG8=", "a-_b", "a+/b", "YQ", "YQ=", "YQ==",
		"text/plain; charset=utf-8", "https://t.me/x", "=", "====", "\x00", "é", "aGVsbG8=\n",
	} {
		f.Add(seed)
	}
	accepts := map[string]func([]byte) bool{
		"any":   nil,
		"utf8":  utf8.Valid,
		"never": func([]byte) bool { return false },
		// Rejecting the first alphabet's result forces the shared buffer to be
		// overwritten by the next one.
		"long": func(b []byte) bool { return len(b) > 2 },
	}
	f.Fuzz(func(t *testing.T, s string) {
		for name, accept := range accepts {
			got, gotEnc, gotOK := Decode(s, accept)
			want, wantEnc, wantOK := referenceDecode(s, accept)
			if gotOK != wantOK || gotEnc != wantEnc || !bytes.Equal(got, want) {
				t.Fatalf("%s: Decode(%q) = %q %v %v, reference %q %v %v", name, s, got, gotEnc, gotOK, want, wantEnc, wantOK)
			}
			if gotOK && cap(got) != len(got) {
				t.Fatalf("%s: result shares spare capacity, an append would clobber the buffer", name)
			}
		}
	})
}
