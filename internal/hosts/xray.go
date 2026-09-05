package hosts

import (
	"encoding/json"
)

// applyXray handles an array of Xray configs, one per host.
func (s *Shuffler) applyXray(body []byte) ([]byte, bool) {
	items, err := parseArray(body)
	if err != nil {
		return body, false
	}

	names := make([]string, len(items))
	for i, item := range items {
		names[i] = xrayName(item)
	}

	perm := s.permutation(names)
	if perm == nil {
		return body, false
	}

	shuffled := make([]json.RawMessage, len(items))
	for slot, from := range perm {
		shuffled[slot] = items[from]
	}
	return indentLike(body, marshalArray(shuffled)), true
}

// xrayName reads the remarks shown to the user.
func xrayName(config json.RawMessage) string {
	var parsed struct {
		Remarks string `json:"remarks"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil {
		return ""
	}
	return parsed.Remarks
}
