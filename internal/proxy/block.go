package proxy

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
)

// scannerExtensions are file types an automated probe asks for. Everything the
// subscription page actually serves is deliberately absent: .js, .css, .map,
// .json, .svg and the image and font types its assets use.
var scannerExtensions = map[string]struct{}{
	".php": {}, ".php3": {}, ".php4": {}, ".php5": {}, ".phtml": {},
	".asp": {}, ".aspx": {}, ".jsp": {}, ".jspx": {},
	".cgi": {}, ".pl": {}, ".py": {}, ".rb": {}, ".sh": {}, ".bat": {},
	".sql": {}, ".sqlite": {}, ".sqlite3": {}, ".db": {}, ".dump": {},
	".bak": {}, ".backup": {}, ".old": {}, ".orig": {}, ".save": {},
	".swp": {}, ".swo": {}, ".tmp": {}, ".temp": {},
	".dist": {}, ".inc": {},
	".ini": {}, ".conf": {}, ".cfg": {}, ".cnf": {}, ".config": {},
	".properties": {}, ".yml": {}, ".yaml": {}, ".toml": {}, ".env": {},
	".log": {}, ".pem": {}, ".key": {}, ".crt": {}, ".p12": {}, ".jks": {}, ".ppk": {},
	".zip": {}, ".tar": {}, ".rar": {}, ".7z": {}, ".war": {}, ".jar": {},
}

// scannerNames are first path segments that never name a subscription. The
// panel mints short UUIDs as nanoids, so none of these words is one in
// practice — but ParseRoute does not enforce that shape, so a deployment that
// somehow hands out such a name has to turn the filter off entirely.
var scannerNames = map[string]struct{}{
	"env": {}, "wordpress": {}, "wp": {}, "wp-admin": {}, "wp-login": {},
	"wp-content": {}, "wp-includes": {},
	"phpmyadmin": {}, "pma": {}, "myadmin": {}, "dbadmin": {}, "sqladmin": {},
	"adminer": {}, "admin": {}, "administrator": {},
	"vendor": {}, "telescope": {}, "actuator": {}, "cgi-bin": {},
	"server-status": {}, "server-info": {}, "_profiler": {}, "_ignition": {},
	"debug": {}, "solr": {}, "jenkins": {}, "manager": {}, "console": {},
	"jmx-console": {}, "druid": {}, "nacos": {}, "geoserver": {},
	"owa": {}, "autodiscover": {}, "ecp": {}, "hnap1": {}, "boaform": {},
	"goform": {}, "struts": {}, "struts2": {},
	"backup": {}, "backups": {}, "dump": {}, "db": {}, "database": {}, "sql": {},
	"config": {}, "configs": {}, "configuration": {}, "settings": {},
	"secrets": {}, "credentials": {}, "shell": {}, "cmd": {},
}

// wellKnown carries ACME challenges and security.txt.
const wellKnown = ".well-known"

// appConfigExt is the extension of the one dotted file the page serves from
// its own assets directory, /assets/.app-config-v2.json.
const appConfigExt = ".json"

// Blocker refuses obvious probes before they reach the upstream, so a scanner
// sweep costs nothing beyond the connection itself.
type Blocker struct {
	enabled bool
	// prefix is CUSTOM_SUB_PREFIX, stripped with the same helper ParseRoute
	// uses, so the two cannot disagree about what a prefixed path is.
	prefix   string
	patterns []*regexp.Regexp
}

// NewBlocker compiles the extra patterns itself: a Block assembled outside the
// config loader would otherwise carry patterns that refuse nothing, in silence.
func NewBlocker(c config.Block, subPrefix string) (*Blocker, error) {
	patterns, err := config.CompileBlock(c)
	if err != nil {
		return nil, err
	}
	return &Blocker{enabled: c.Enabled, prefix: subPrefix, patterns: patterns}, nil
}

// Blocked reports whether path should be refused. The path arrives already
// percent-decoded, so an encoded dot cannot slip past.
func (b *Blocker) Blocked(path string) bool {
	if b == nil || !b.enabled {
		return false
	}

	for _, re := range b.patterns {
		if re.MatchString(path) {
			return true
		}
	}

	segments := splitPath(path)
	if len(segments) == 0 {
		return false
	}

	// Every segment is weighed, not just the last one: /index.php/x reaches
	// the same file as /index.php, and /dump.sql/ the same as /dump.sql, once
	// the upstream has normalised the path.
	for _, segment := range segments {
		if _, hit := scannerExtensions[extension(segment)]; hit {
			return true
		}
	}

	// The name check runs on the path the page sees, not on the prefix an
	// operator may have chosen — CUSTOM_SUB_PREFIX=admin must keep working.
	named, _ := stripPrefix(segments, b.prefix)
	if len(named) == 0 {
		return false
	}
	if _, hit := scannerNames[strings.ToLower(named[0])]; hit {
		return true
	}

	if inAssets {
		return false
	}
	for _, segment := range segments {
		if strings.HasPrefix(segment, ".") && segment != wellKnown {
			return true
		}
		if i == 0 && strings.EqualFold(segment, wellKnown) {
			continue
		}
		// The page serves exactly one dotted file of its own, so the exemption
		// reaches no further than a JSON name directly inside /assets.
		if i == 1 && i == len(named)-1 &&
			strings.EqualFold(named[0], assetsDir) &&
			extension(segment) == appConfigExt {
			continue
		}
		return true
	}
	return false
}

// extension is the lowercased suffix of a path segment, "" when it has no dot.
// A leading dot counts: ".env" is an extension in its own right.
func extension(segment string) string {
	i := strings.LastIndexByte(segment, '.')
	if i < 0 {
		return ""
	}
	return strings.ToLower(segment[i:])
}

// refuse answers without touching the upstream. 404 is deliberate: it says
// nothing about what does exist here.
func refuse(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
}
