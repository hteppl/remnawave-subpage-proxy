package hosts

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// object is a JSON object that keeps its member order.
type object []member

type member struct {
	key   string
	value json.RawMessage
}

func parseObject(raw []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}

	var obj object
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("object key is not a string")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		obj = append(obj, member{key: key, value: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return obj, nil
}

func (o object) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

func (o object) set(key string, value json.RawMessage) {
	for i := range o {
		if o[i].key == key {
			o[i].value = value
			return
		}
	}
}

func (o object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(m.key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(m.value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func parseArray(raw []byte) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// marshalArray joins raw elements without reformatting them.
func marshalArray(items []json.RawMessage) []byte {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(item)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

// indentLike formats out with the indentation original used.
func indentLike(original, out []byte) []byte {
	var buf bytes.Buffer
	indent := detectIndent(original)
	if indent == "" {
		if err := json.Compact(&buf, out); err != nil {
			return out
		}
		return buf.Bytes()
	}
	if err := json.Indent(&buf, out, "", indent); err != nil {
		return out
	}
	return buf.Bytes()
}

// detectIndent reads the first nested line's indentation, "" if single-line.
func detectIndent(raw []byte) string {
	nl := bytes.IndexByte(raw, '\n')
	if nl < 0 {
		return ""
	}
	rest := raw[nl+1:]
	n := 0
	for n < len(rest) && (rest[n] == ' ' || rest[n] == '\t') {
		n++
	}
	return string(rest[:n])
}
