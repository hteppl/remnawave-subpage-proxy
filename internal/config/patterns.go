package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Block refuses scanner probes before they reach the subscription page or panel.
type Block struct {
	Enabled bool `yaml:"enabled"`
	// Patterns are extra Go regular expressions matched against the path.
	Patterns []string `yaml:"patterns"`
}

// Hosts controls shuffling of the servers inside a subscription body.
type Hosts struct {
	// Shuffle regexps match the client-visible name; non-matching hosts stay put, multi-matches join the first group.
	Shuffle []string `yaml:"shuffle"`
}

// CompileBlock is shared by the loader and the proxy so they cannot disagree on a pattern.
func CompileBlock(b Block) ([]*regexp.Regexp, error) {
	return compilePatterns("block.patterns", b.Patterns)
}

func CompileHosts(h Hosts) ([]*regexp.Regexp, error) {
	return compilePatterns("hosts.shuffle", h.Shuffle)
}

// compilePatterns rejects blank patterns, which would match everything.
func compilePatterns(field string, patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	var problems []error
	for i, pattern := range patterns {
		re, err := compilePattern(pattern)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s[%d] %w", field, i, err))
			continue
		}
		compiled = append(compiled, re)
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return compiled, nil
}

func compilePattern(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, errors.New("needs a pattern")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("is not a valid regexp: %w", err)
	}
	return re, nil
}

func problemList(err error) []string {
	if err == nil {
		return nil
	}
	return strings.Split(err.Error(), "\n")
}
