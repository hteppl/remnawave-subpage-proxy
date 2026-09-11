package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type UserAgentAction string

const (
	// UserAgentNotice forwards and keeps panel headers but swaps hosts for message placeholders.
	UserAgentNotice UserAgentAction = "notice"
	// UserAgentBlock answers with the placeholders without forwarding the request.
	UserAgentBlock UserAgentAction = "block"
)

func (a *UserAgentAction) UnmarshalYAML(node *yaml.Node) error {
	return decodeEnum(node, a, "user_agents action", UserAgentNotice, UserAgentBlock)
}

// UserAgentRule catches a User-Agent no real client sends, such as a pasted URL or JSON config.
type UserAgentRule struct {
	// Name is logged instead of the User-Agent, which may hold a subscription link.
	Name string `yaml:"name"`
	// Pattern is a Go regexp matched against the whole User-Agent.
	Pattern string          `yaml:"pattern"`
	Action  UserAgentAction `yaml:"action"`
	// Message lines each become a placeholder host; a rule without one is disabled.
	Message string `yaml:"message"`
	// Announce also sends the message as the announce header.
	Announce bool `yaml:"announce"`
}

type UserAgents struct {
	// Rules are tried in order; the first match decides.
	Rules []UserAgentRule `yaml:"rules"`
}

type CompiledUserAgentRule struct {
	UserAgentRule
	Regexp *regexp.Regexp
}

// CompileUserAgents drops rules without a message into disabled; there is no
// default text since any wording would be wrong for someone.
func CompileUserAgents(u UserAgents) (compiled []CompiledUserAgentRule, disabled []string, err error) {
	var problems []error
	for i, rule := range u.Rules {
		label := fmt.Sprintf("user_agents.rules[%d]", i)
		if rule.Name = strings.TrimSpace(rule.Name); rule.Name != "" {
			label += " (" + rule.Name + ")"
		} else {
			rule.Name = fmt.Sprintf("rule-%d", i)
		}
		re, err := compilePattern(rule.Pattern)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s pattern %w", label, err))
			continue
		}
		if rule.Action == "" {
			rule.Action = UserAgentNotice
		}
		if strings.TrimSpace(rule.Message) == "" {
			disabled = append(disabled, label)
			continue
		}
		compiled = append(compiled, CompiledUserAgentRule{UserAgentRule: rule, Regexp: re})
	}
	if len(problems) > 0 {
		return nil, nil, errors.Join(problems...)
	}
	return compiled, disabled, nil
}
