package proxy

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
)

// Everything the page serves (.js, .css, .map, .json, .svg, images, fonts) is deliberately absent.
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

// ParseRoute does not enforce the nanoid shape, so a subscription named like one of these needs the filter off.
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

// The one dotted file the page serves: /assets/.app-config-v2.json.
const appConfigExt = ".json"

// Blocker refuses obvious probes before they reach the upstream.
type Blocker struct {
	enabled bool
	// Stripped with ParseRoute's helper so the two agree on what a prefixed path is.
	prefix   string
	patterns []*regexp.Regexp
}

// NewBlocker compiles the patterns itself, so a hand-built Block cannot silently refuse nothing.
func NewBlocker(c config.Block, subPrefix string) (*Blocker, error) {
	patterns, err := config.CompileBlock(c)
	if err != nil {
		return nil, err
	}
	return &Blocker{enabled: c.Enabled, prefix: subPrefix, patterns: patterns}, nil
}

// Blocked expects a percent-decoded path, so an encoded dot cannot slip past.
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

	// Every segment is weighed: the upstream normalises /index.php/x to /index.php.
	for _, segment := range segments {
		if _, hit := scannerExtensions[extension(segment)]; hit {
			return true
		}
	}

	// Runs after the prefix is stripped so CUSTOM_SUB_PREFIX=admin keeps working.
	named, _ := stripPrefix(segments, b.prefix)
	if len(named) == 0 {
		return false
	}
	if _, hit := scannerNames[strings.ToLower(named[0])]; hit {
		return true
	}

	for i, segment := range named {
		if !strings.HasPrefix(segment, ".") {
			continue
		}
		// The upstream resolves "." and "..", so no exemption below may let one through.
		if segment == "." || segment == ".." {
			return true
		}
		if i == 0 && strings.EqualFold(segment, wellKnown) {
			continue
		}
		// The exemption covers only a JSON name directly inside /assets.
		if i == 1 && i == len(named)-1 &&
			strings.EqualFold(named[0], assetsDir) &&
			extension(segment) == appConfigExt {
			continue
		}
		return true
	}
	return false
}

// A leading dot counts: ".env" is an extension in its own right.
func extension(segment string) string {
	i := strings.LastIndexByte(segment, '.')
	if i < 0 {
		return ""
	}
	return strings.ToLower(segment[i:])
}

// 404 is deliberate: it reveals nothing about what exists here.
func refuse(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
}
