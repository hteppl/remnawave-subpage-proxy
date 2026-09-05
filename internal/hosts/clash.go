package hosts

import (
	"bytes"
	"regexp"

	"gopkg.in/yaml.v3"
)

var clashProxiesKey = regexp.MustCompile(`(?m)^proxies:`)

func looksLikeClash(body []byte) bool {
	return clashProxiesKey.Match(body)
}

// applyClash shuffles the proxies list and reorders proxy-group names. The
// node tree keeps comments and style intact.
func (s *Shuffler) applyClash(body []byte) ([]byte, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil || len(doc.Content) == 0 {
		return body, false
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return body, false
	}

	proxies := mappingValue(root, "proxies")
	if proxies == nil || proxies.Kind != yaml.SequenceNode {
		return body, false
	}

	names := make([]string, len(proxies.Content))
	for i, proxy := range proxies.Content {
		names[i] = scalarValue(mappingValue(proxy, "name"))
	}

	perm := s.permutation(names)
	if perm == nil {
		return body, false
	}

	shuffled := make([]*yaml.Node, len(proxies.Content))
	newIndex := make(map[string]int)
	for slot, from := range perm {
		shuffled[slot] = proxies.Content[from]
		if s.group(names[from]) >= 0 {
			newIndex[names[from]] = slot
		}
	}
	proxies.Content = shuffled

	if groups := mappingValue(root, "proxy-groups"); groups != nil && groups.Kind == yaml.SequenceNode {
		for _, group := range groups.Content {
			members := mappingValue(group, "proxies")
			if members == nil || members.Kind != yaml.SequenceNode {
				continue
			}
			list := make([]string, len(members.Content))
			for i, m := range members.Content {
				list[i] = scalarValue(m)
			}
			if !reorderNames(list, newIndex) {
				continue
			}
			byName := make(map[string]*yaml.Node, len(members.Content))
			for _, m := range members.Content {
				byName[scalarValue(m)] = m
			}
			for i, name := range list {
				members.Content[i] = byName[name]
			}
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return body, false
	}
	_ = enc.Close()
	return buf.Bytes(), true
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func scalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}
