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

	hosts := make([]Host, len(items))
	for i, item := range items {
		hosts[i] = xrayHost(item)
	}

	perm := s.permutation(hosts)
	if perm == nil {
		return body, false
	}

	shuffled := make([]json.RawMessage, len(items))
	for slot, from := range perm {
		shuffled[slot] = items[from]
	}
	return indentLike(body, marshalArray(shuffled)), true
}

// xrayHost reads remarks and the first outbound's server.
func xrayHost(config json.RawMessage) Host {
	var parsed struct {
		Remarks   string `json:"remarks"`
		Outbounds []struct {
			Settings struct {
				Vnext []struct {
					Address string `json:"address"`
				} `json:"vnext"`
				Servers []struct {
					Address string `json:"address"`
				} `json:"servers"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil {
		return Host{}
	}
	host := Host{Name: parsed.Remarks}
	for _, ob := range parsed.Outbounds {
		for _, v := range ob.Settings.Vnext {
			if v.Address != "" {
				host.Hostname = v.Address
				return host
			}
		}
		for _, v := range ob.Settings.Servers {
			if v.Address != "" {
				host.Hostname = v.Address
				return host
			}
		}
	}
	return host
}
