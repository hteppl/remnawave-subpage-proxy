package version

// Injected via -ldflags "-X ...".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func UserAgent() string {
	return "remnawave-subpage-proxy/" + Version
}
