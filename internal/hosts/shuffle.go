// Package hosts shuffles the servers inside a subscription body.
package hosts

import (
	"bytes"
	"math/rand/v2"
	"regexp"
	"sort"
)

// Shuffler is safe for concurrent use.
type Shuffler struct {
	groups []*regexp.Regexp
	// shuffle is rand.Shuffle; tests replace it.
	shuffle func(n int, swap func(i, j int))
}

// New builds a Shuffler; no groups means it changes nothing.
func New(groups []*regexp.Regexp) *Shuffler {
	return &Shuffler{groups: groups, shuffle: rand.Shuffle}
}

// Enabled reports whether Apply can ever change a body.
func (s *Shuffler) Enabled() bool {
	return s != nil && len(s.groups) > 0
}

// Apply sniffs the body format and returns an unknown body untouched.
func (s *Shuffler) Apply(body []byte) ([]byte, bool) {
	if !s.Enabled() || len(body) == 0 {
		return body, false
	}

	switch Sniff(body) {
	case FormatXray:
		return s.applyXray(body)
	case FormatSingbox:
		return s.applySingbox(body)
	case FormatClash:
		return s.applyClash(body)
	case FormatLinks:
		return s.applyLinks(body)
	default:
		return body, false
	}
}

type Format int

const (
	FormatUnknown Format = iota
	// FormatLinks is one link per line, plain or base64-wrapped.
	FormatLinks
	FormatXray
	FormatSingbox
	// FormatClash covers Clash, Mihomo and Stash YAML.
	FormatClash
)

// Sniff looks at the body only; empty is FormatUnknown and anything unrecognised is taken as links.
func Sniff(body []byte) Format {
	switch trimmed := bytes.TrimSpace(body); {
	case len(trimmed) == 0:
		return FormatUnknown
	case trimmed[0] == '[':
		return FormatXray
	case trimmed[0] == '{':
		return FormatSingbox
	case looksLikeClash(trimmed):
		return FormatClash
	default:
		return FormatLinks
	}
}

// FormatForClientType is the fallback when the body itself cannot be read.
func FormatForClientType(clientType string) Format {
	switch clientType {
	case "json", "v2ray-json":
		return FormatXray
	case "singbox":
		return FormatSingbox
	case "clash", "mihomo", "stash":
		return FormatClash
	default:
		return FormatLinks
	}
}

// group matches the client-visible name and returns -1 for none.
func (s *Shuffler) group(name string) int {
	if name == "" {
		return -1
	}
	for i, g := range s.groups {
		if g.MatchString(name) {
			return i
		}
	}
	return -1
}

// permutation maps each slot to the entry now filling it, shuffling only within a group; nil means nothing moved.
func (s *Shuffler) permutation(names []string) []int {
	slots := make(map[int][]int)
	for i, name := range names {
		if g := s.group(name); g >= 0 {
			slots[g] = append(slots[g], i)
		}
	}
	if len(slots) == 0 {
		return nil
	}

	perm := make([]int, len(names))
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
