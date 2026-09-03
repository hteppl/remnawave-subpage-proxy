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
var scannerExtensions = []string{
	".php", ".php3", ".php4", ".php5", ".phtml", ".asp", ".aspx", ".jsp", ".jspx",
	".cgi", ".pl", ".py", ".rb", ".sh", ".bat",
	".sql", ".sqlite", ".sqlite3", ".db", ".dump",
	".bak", ".backup", ".old", ".orig", ".save", ".swp", ".swo", ".tmp", ".temp",
	".dist", ".inc",
	".ini", ".conf", ".cfg", ".cnf", ".config", ".properties", ".yml", ".yaml",
	".toml", ".env",
	".log", ".pem", ".key", ".crt", ".p12", ".jks", ".ppk",
	".zip", ".tar", ".rar", ".7z", ".war", ".jar",
}

// scannerNames are first path segments that never name a subscription. Short
// UUIDs are 16-character nanoids, so none of these can collide with one.
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

const (
	// wellKnown carries ACME challenges and security.txt.
	wellKnown = ".well-known"
	// assetsDir is where the page serves its own static files, including
	// /assets/.app-config-v2.json — a dotted name that must stay reachable.
	assetsDir = "assets"
)

// Blocker refuses obvious probes before they reach the upstream, so a scanner
// sweep costs nothing beyond the connection itself.
type Blocker struct {
	enabled   bool
	subPrefix string
	patterns  []*regexp.Regexp
}

func NewBlocker(c config.Block, subPrefix string) *Blocker {
	return &Blocker{enabled: c.Enabled, subPrefix: subPrefix, patterns: c.Compiled()}
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

	lower := strings.ToLower(path)
	for _, ext := range scannerExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}

	segments := splitPath(path)
	if len(segments) == 0 {
		return false
	}

	// The page serves its own static files, one of which is a dotted name, so
	// the dotfile rule does not apply inside that directory.
	inAssets := strings.EqualFold(segments[0], assetsDir)

	// The name check runs on the path the page sees, not on the prefix an
	// operator may have chosen — CUSTOM_SUB_PREFIX=admin must keep working.
	named := segments
	if b.subPrefix != "" && strings.EqualFold(segments[0], b.subPrefix) {
		named = segments[1:]
	}
	if len(named) > 0 {
		if _, hit := scannerNames[strings.ToLower(named[0])]; hit {
			return true
		}
	}

	if inAssets {
		return false
	}
	for _, segment := range segments {
		if strings.HasPrefix(segment, ".") && segment != wellKnown {
			return true
		}
	}
	return false
}

func splitPath(path string) []string {
	var out []string
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}

// refuse answers without touching the upstream. 404 is deliberate: it says
// nothing about what does exist here.
func refuse(w http.ResponseWriter) {
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusNotFound)
}
