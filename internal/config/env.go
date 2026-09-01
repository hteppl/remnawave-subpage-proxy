package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// envError reports every problem at once instead of one per restart.
type envError struct {
	problems []string
}

func (e *envError) addf(format string, args ...any) {
	e.problems = append(e.problems, fmt.Sprintf(format, args...))
}

func (e *envError) Error() string {
	return "invalid configuration:\n  - " + strings.Join(e.problems, "\n  - ")
}

func (e *envError) orNil() error {
	if len(e.problems) == 0 {
		return nil
	}
	return e
}

func lookup(key string) (string, bool) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	return raw, true
}

func envString(key, def string) string {
	if raw, ok := lookup(key); ok {
		return raw
	}
	return def
}

func (e *envError) requiredString(key string) string {
	raw, ok := lookup(key)
	if !ok {
		e.addf("%s is required", key)
		return ""
	}
	return raw
}

func (e *envError) duration(key string, def time.Duration) time.Duration {
	raw, ok := lookup(key)
	if !ok {
		return def
	}
	// Bare numbers are read as seconds; anything else must be a Go duration.
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs < 0 {
			e.addf("%s must not be negative, got %q", key, raw)
			return def
		}
		return time.Duration(secs) * time.Second
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		e.addf("%s must be a duration such as 30s or 5m, got %q", key, raw)
		return def
	}
	if d < 0 {
		e.addf("%s must not be negative, got %q", key, raw)
		return def
	}
	return d
}

func (e *envError) intVal(key string, def int) int {
	raw, ok := lookup(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		e.addf("%s must be an integer, got %q", key, raw)
		return def
	}
	return n
}

func (e *envError) boolVal(key string, def bool) bool {
	raw, ok := lookup(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		e.addf("%s must be true or false, got %q", key, raw)
		return def
	}
	return b
}

// Longest suffix first, so KIB is not read as B.
var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"KIB", 1 << 10}, {"MIB", 1 << 20}, {"GIB", 1 << 30},
	{"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000},
	{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30},
	{"B", 1},
}

func (e *envError) sizeVal(key string, def int64) int64 {
	raw, ok := lookup(key)
	if !ok {
		return def
	}
	n, err := parseSize(raw)
	if err != nil {
		e.addf("%s must be a byte size such as 1MiB, 512KB or 1048576, got %q", key, raw)
		return def
	}
	if n < 0 {
		e.addf("%s must not be negative, got %q", key, raw)
		return def
	}
	return n
}

func parseSize(raw string) (int64, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	mult := int64(1)
	for _, unit := range sizeUnits {
		if rest, found := strings.CutSuffix(s, unit.suffix); found {
			s, mult = strings.TrimSpace(rest), unit.mult
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return n * mult, nil
}
