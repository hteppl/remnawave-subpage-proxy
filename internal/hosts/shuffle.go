// Package hosts shuffles the servers inside a subscription body.
package hosts

import (
	"bytes"
	"math/rand/v2"
	"sort"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
)

// Host is what a shuffle group is matched against.
type Host struct {
	Hostname string
	Name     string
}

// Shuffler permutes the hosts of a subscription. Safe for concurrent use.
type Shuffler struct {
	groups []config.CompiledShuffleGroup
	// shuffle is rand.Shuffle; tests replace it.
	shuffle func(n int, swap func(i, j int))
}

// New builds a Shuffler; no groups means it changes nothing.
func New(groups []config.CompiledShuffleGroup) *Shuffler {
	return &Shuffler{groups: groups, shuffle: rand.Shuffle}
}

// Enabled reports whether Apply can ever change a body.
func (s *Shuffler) Enabled() bool {
	return s != nil && len(s.groups) > 0
}

// Apply shuffles the hosts of body and reports whether it changed. The format
// (Xray array, sing-box, Clash YAML, plain or base64 links) is sniffed; an
// unknown body is returned untouched.
func (s *Shuffler) Apply(body []byte) ([]byte, bool) {
	if !s.Enabled() || len(body) == 0 {
		return body, false
	}

	switch trimmed := bytes.TrimSpace(body); {
	case len(trimmed) == 0:
		return body, false
	case trimmed[0] == '[':
		return s.applyXray(body)
	case trimmed[0] == '{':
		return s.applySingbox(body)
	case looksLikeClash(trimmed):
		return s.applyClash(body)
	default:
		return s.applyLinks(body)
	}
}

// group returns the index of the first matching group, -1 for none.
func (s *Shuffler) group(host Host) int {
	if host.Hostname == "" && host.Name == "" {
		return -1
	}
	for i, g := range s.groups {
		if g.Matches(host.Hostname, host.Name) {
			return i
		}
	}
	return -1
}

// permutation shuffles each group among its own slots. The result maps a
// slot to the entry now filling it; nil means nothing moved.
func (s *Shuffler) permutation(hosts []Host) []int {
	slots := make(map[int][]int)
	for i, host := range hosts {
		if g := s.group(host); g >= 0 {
			slots[g] = append(slots[g], i)
		}
	}

	perm := make([]int, len(hosts))
	for i := range perm {
		perm[i] = i
	}

	// Fixed order keeps a seeded shuffle repeatable.
	groups := make([]int, 0, len(slots))
	for g := range slots {
		groups = append(groups, g)
	}
	sort.Ints(groups)

	moved := false
	for _, g := range groups {
		members := slots[g]
		if len(members) < 2 {
			continue
		}
		order := append([]int(nil), members...)
		s.shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		for k, slot := range members {
			perm[slot] = order[k]
			if order[k] != slot {
				moved = true
			}
		}
	}
	if !moved {
		return nil
	}
	return perm
}

// reorderNames makes a by-name list (Clash group, sing-box selector) follow
// the new host order; names not in newIndex (DIRECT, other groups) stay put.
func reorderNames(names []string, newIndex map[string]int) bool {
	var slots []int
	for i, name := range names {
		if _, ok := newIndex[name]; ok {
			slots = append(slots, i)
		}
	}
	if len(slots) < 2 {
		return false
	}

	picked := make([]string, len(slots))
	for k, slot := range slots {
		picked[k] = names[slot]
	}
	sort.SliceStable(picked, func(a, b int) bool {
		return newIndex[picked[a]] < newIndex[picked[b]]
	})

	changed := false
	for k, slot := range slots {
		if names[slot] != picked[k] {
			changed = true
		}
		names[slot] = picked[k]
	}
	return changed
}
