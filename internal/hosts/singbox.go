package hosts

import (
	"encoding/json"
)

// applySingbox shuffles node outbounds and reorders selector tag lists.
func (s *Shuffler) applySingbox(body []byte) ([]byte, bool) {
	root, err := parseObject(body)
	if err != nil {
		return body, false
	}
	rawOutbounds, ok := root.get("outbounds")
	if !ok {
		return body, false
	}
	outbounds, err := parseArray(rawOutbounds)
	if err != nil {
		return body, false
	}

	type node struct {
		Tag       string   `json:"tag"`
		Server    string   `json:"server"`
		Outbounds []string `json:"outbounds"`
	}
	nodes := make([]node, len(outbounds))
	names := make([]string, len(outbounds))
	for i, raw := range outbounds {
		_ = json.Unmarshal(raw, &nodes[i])
		// A tag only names a host when the outbound has a server; selectors
		// and direct/block carry tags too, and must not be shuffled.
		if nodes[i].Server != "" {
			names[i] = nodes[i].Tag
		}
	}

	perm := s.permutation(names)
	if perm == nil {
		return body, false
	}

	shuffled := make([]json.RawMessage, len(outbounds))
	newIndex := make(map[string]int)
	for slot, from := range perm {
		shuffled[slot] = outbounds[from]
		if s.group(names[from]) >= 0 {
			newIndex[names[from]] = slot
		}
	}

	for i, raw := range shuffled {
		var n node
		if err := json.Unmarshal(raw, &n); err != nil || len(n.Outbounds) == 0 {
			continue
		}
		if !reorderNames(n.Outbounds, newIndex) {
			continue
		}
		obj, err := parseObject(raw)
		if err != nil {
			continue
		}
		list, err := json.Marshal(n.Outbounds)
		if err != nil {
			continue
		}
		obj.set("outbounds", list)
		if encoded, err := json.Marshal(obj); err == nil {
			shuffled[i] = encoded
		}
	}

	root.set("outbounds", marshalArray(shuffled))
	out, err := json.Marshal(root)
	if err != nil {
		return body, false
	}
	return indentLike(body, out), true
}
