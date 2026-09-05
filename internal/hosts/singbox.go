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
	hosts := make([]Host, len(outbounds))
	for i, raw := range outbounds {
		_ = json.Unmarshal(raw, &nodes[i])
		// Only outbounds with a server are hosts.
		if nodes[i].Server != "" {
			hosts[i] = Host{Hostname: nodes[i].Server, Name: nodes[i].Tag}
		}
	}

	perm := s.permutation(hosts)
	if perm == nil {
		return body, false
	}

	shuffled := make([]json.RawMessage, len(outbounds))
	newIndex := make(map[string]int)
	for slot, from := range perm {
		shuffled[slot] = outbounds[from]
		if s.group(hosts[from]) >= 0 && nodes[from].Tag != "" {
			newIndex[nodes[from].Tag] = slot
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
