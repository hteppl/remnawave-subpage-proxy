package subinfo

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/panel"
)

// Origin says where a value can come from, so the panel call can be skipped.
type Origin uint8

const (
	// OriginLocal comes from the request and static config alone.
	OriginLocal Origin = iota
	// OriginTraffic prefers the response header, falling back to the panel.
	OriginTraffic
	// OriginPanel is only available from the panel API.
	OriginPanel
)

// Catalog is the full set of built-in placeholders.
var Catalog = map[string]Origin{
	"TRAFFIC_USED":            OriginTraffic,
	"TRAFFIC_USED_BYTES":      OriginTraffic,
	"TRAFFIC_LIMIT":           OriginTraffic,
	"TRAFFIC_LIMIT_BYTES":     OriginTraffic,
	"TRAFFIC_AVAILABLE":       OriginTraffic,
	"TRAFFIC_AVAILABLE_BYTES": OriginTraffic,
	"TRAFFIC_USED_PERCENT":    OriginTraffic,
	"TRAFFIC_LEFT_PERCENT":    OriginTraffic,
	"PROGRESS_BAR":            OriginTraffic,
	"TRAFFIC_UPLOAD":          OriginTraffic,
	"TRAFFIC_DOWNLOAD":        OriginTraffic,

	"DAYS_LEFT":       OriginTraffic,
	"EXPIRES_AT":      OriginTraffic,
	"EXPIRES_AT_DATE": OriginTraffic,
	"EXPIRES_AT_TIME": OriginTraffic,
	"EXPIRES_AT_UNIX": OriginTraffic,

	"USERNAME":               OriginPanel,
	"USER_STATUS":            OriginPanel,
	"IS_ACTIVE":              OriginPanel,
	"TRAFFIC_LIMIT_STRATEGY": OriginPanel,
	"LIFETIME_TRAFFIC_USED":  OriginPanel,

	"SHORT_UUID":       OriginLocal,
	"SUBSCRIPTION_URL": OriginLocal,
	"CLIENT_TYPE":      OriginLocal,
	"USER_AGENT":       OriginLocal,
	"CLIENT_IP":        OriginLocal,
	"NOW":              OriginLocal,
	"DATE":             OriginLocal,
	"TIME":             OriginLocal,
}

type Source struct {
	ShortUUID  string
	ClientType string
	UserAgent  string
	ClientIP   string
	// SubscriptionURL is rebuilt from the request, so it costs no panel lookup.
	SubscriptionURL string

	UserInfo *UserInfo
	Panel    *panel.Info
}

type Resolver struct {
	traffic        config.TrafficFormat
	forceUnlimited bool
	datetime       config.DateTimeFormat
	bar            config.ProgressBar
	custom         map[string]string

	now func() time.Time
}

func NewResolver(f config.File) *Resolver {
	custom := make(map[string]string, len(f.Vars))
	for k, v := range f.Vars {
		custom[k] = v
	}
	return &Resolver{
		traffic:        f.Traffic,
		forceUnlimited: f.Traffic.ForceUnlimited,
		datetime:       f.DateTime,
		bar:            f.ProgressBar,
		custom:         custom,
		now:            time.Now,
	}
}

// NeedsPanel reports whether any name still requires the panel API.
func (r *Resolver) NeedsPanel(names []string, haveUserInfo bool) bool {
	for _, name := range names {
		if _, isCustom := r.custom[name]; isCustom {
			continue
		}
		switch Catalog[name] {
		case OriginPanel:
			return true
		case OriginTraffic:
			if !haveUserInfo {
				return true
			}
		}
	}
	return false
}

func (r *Resolver) Lookup(src Source) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if v, ok := r.custom[name]; ok {
			return v, true
		}
		return r.resolve(name, src)
	}
}

func (r *Resolver) resolve(name string, src Source) (string, bool) {
	switch name {
	case "SHORT_UUID":
		return src.ShortUUID, src.ShortUUID != ""
	case "CLIENT_TYPE":
		return src.ClientType, src.ClientType != ""
	case "USER_AGENT":
		return src.UserAgent, src.UserAgent != ""
	case "CLIENT_IP":
		return src.ClientIP, src.ClientIP != ""
	case "SUBSCRIPTION_URL":
		return src.SubscriptionURL, src.SubscriptionURL != ""
	case "NOW":
		now := r.now().In(r.datetime.Location())
		return now.Format(r.datetime.Layout + " " + r.datetime.TimeLayout), true
	case "DATE":
		return r.now().In(r.datetime.Location()).Format(r.datetime.Layout), true
	case "TIME":
		return r.now().In(r.datetime.Location()).Format(r.datetime.TimeLayout), true

	case "USERNAME":
		if src.Panel == nil {
			return "", false
		}
		return src.Panel.User.Username, src.Panel.User.Username != ""
	case "USER_STATUS":
		if src.Panel == nil {
			return "", false
		}
		return src.Panel.User.UserStatus, src.Panel.User.UserStatus != ""
	case "IS_ACTIVE":
		if src.Panel == nil {
			return "", false
		}
		return strconv.FormatBool(src.Panel.User.IsActive), true
	case "TRAFFIC_LIMIT_STRATEGY":
		if src.Panel == nil {
			return "", false
		}
		return src.Panel.User.TrafficLimitStrategy, src.Panel.User.TrafficLimitStrategy != ""
	case "LIFETIME_TRAFFIC_USED":
		if src.Panel == nil {
			return "", false
		}
		return r.bytes(src.Panel.User.LifetimeUsedBytes())
	}

	used, limit, ok := r.trafficCounters(src)
	if !ok {
		// Fall through to expiry, which may still be answerable.
		used, limit = -1, -1
	}

	switch name {
	case "TRAFFIC_USED":
		return r.bytes(used)
	case "TRAFFIC_USED_BYTES":
		return r.rawBytes(used)
	case "TRAFFIC_LIMIT":
		if limit == 0 {
			return r.traffic.Unlimited, true
		}
		return r.bytes(limit)
	case "TRAFFIC_LIMIT_BYTES":
		return r.rawBytes(limit)
	case "TRAFFIC_AVAILABLE":
		if limit == 0 {
			return r.traffic.Unlimited, true
		}
		return r.bytes(available(used, limit))
	case "TRAFFIC_AVAILABLE_BYTES":
		if limit == 0 {
			return "0", true
		}
		return r.rawBytes(available(used, limit))
	case "TRAFFIC_USED_PERCENT":
		pct, ok := percentUsed(used, limit)
		if !ok {
			return "", false
		}
		return strconv.Itoa(pct), true
	case "TRAFFIC_LEFT_PERCENT":
		pct, ok := percentUsed(used, limit)
		if !ok {
			return "", false
		}
		return strconv.Itoa(100 - pct), true
	case "PROGRESS_BAR":
		pct, ok := percentUsed(used, limit)
		if !ok {
			return "", false
		}
		return r.progressBar(pct), true
	case "TRAFFIC_UPLOAD":
		if src.UserInfo == nil {
			return "", false
		}
		return r.bytes(src.UserInfo.Upload)
	case "TRAFFIC_DOWNLOAD":
		if src.UserInfo == nil {
			return "", false
		}
		return r.bytes(src.UserInfo.Download)
	}

	expiry, hasExpiry := r.expiry(src)
	switch name {
	case "DAYS_LEFT":
		if src.Panel != nil {
			return strconv.Itoa(max(0, src.Panel.User.DaysLeft)), true
		}
		if !hasExpiry {
			return "", false
		}
		return strconv.Itoa(daysUntil(r.now(), expiry)), true
	case "EXPIRES_AT":
		if !hasExpiry {
			return r.datetime.Never, true
		}
		return expiry.In(r.datetime.Location()).Format(r.datetime.Layout + " " + r.datetime.TimeLayout), true
	case "EXPIRES_AT_DATE":
		if !hasExpiry {
			return r.datetime.Never, true
		}
		return expiry.In(r.datetime.Location()).Format(r.datetime.Layout), true
	case "EXPIRES_AT_TIME":
		if !hasExpiry {
			return r.datetime.Never, true
		}
		return expiry.In(r.datetime.Location()).Format(r.datetime.TimeLayout), true
	case "EXPIRES_AT_UNIX":
		if !hasExpiry {
			return "0", true
		}
		return strconv.FormatInt(expiry.Unix(), 10), true
	}

	return "", false
}

// trafficCounters prefers the response header: consistent with the payload the
// client just received, and free.
func (r *Resolver) trafficCounters(src Source) (used, limit int64, ok bool) {
	used, limit = -1, -1

	if src.UserInfo != nil {
		u, l := src.UserInfo.Used(), src.UserInfo.Total
		if u >= 0 || l >= 0 {
			used, limit, ok = u, l, true
		}
	}
	if !ok && src.Panel != nil {
		used, limit, ok = src.Panel.User.UsedBytes(), src.Panel.User.LimitBytes(), true
	}

	// A zero limit already means unlimited everywhere below, and the answer does
	// not depend on having any counters.
	if r.forceUnlimited {
		limit, ok = 0, true
	}
	return used, limit, ok
}

func (r *Resolver) expiry(src Source) (time.Time, bool) {
	if src.UserInfo != nil && src.UserInfo.Expire > 0 {
		return time.Unix(src.UserInfo.Expire, 0), true
	}
	if src.Panel != nil {
		if t, ok := src.Panel.User.Expiry(); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func (r *Resolver) bytes(n int64) (string, bool) {
	if n < 0 {
		return "", false
	}
	return FormatBytes(n, r.traffic), true
}

func (r *Resolver) rawBytes(n int64) (string, bool) {
	if n < 0 {
		return "", false
	}
	return strconv.FormatInt(n, 10), true
}

func (r *Resolver) progressBar(pct int) string {
	width := r.bar.Width
	if width <= 0 {
		return ""
	}
	filled := int(math.Round(float64(width) * float64(pct) / 100))
	filled = min(max(filled, 0), width)
	return strings.Repeat(r.bar.Filled, filled) + strings.Repeat(r.bar.Empty, width-filled)
}

func available(used, limit int64) int64 {
	if used < 0 || limit < 0 {
		return -1
	}
	return max(0, limit-used)
}

// percentUsed reads an unlimited plan as 0%, so progress bars still render.
func percentUsed(used, limit int64) (int, bool) {
	switch {
	case used < 0 || limit < 0:
		return 0, false
	case limit == 0:
		return 0, true
	default:
		pct := int(math.Round(float64(used) / float64(limit) * 100))
		return min(max(pct, 0), 100), true
	}
}

func daysUntil(now, expiry time.Time) int {
	if !expiry.After(now) {
		return 0
	}
	return int(math.Ceil(expiry.Sub(now).Hours() / 24))
}

var (
	decimalUnits = []string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}
	binaryUnits  = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
)

// FormatBytes gives whole bytes no decimals: "512.00 B" reads as a bug.
func FormatBytes(n int64, f config.TrafficFormat) string {
	units := decimalUnits
	base := int64(1000)
	if f.BinaryUnits {
		units, base = binaryUnits, 1024
	}
	if len(f.Units) > 0 {
		units = f.Units
	}

	if n < base {
		return strconv.FormatInt(n, 10) + " " + units[0]
	}

	value := float64(n)
	idx := 0
	for value >= float64(base) && idx < len(units)-1 {
		value /= float64(base)
		idx++
	}
	return strconv.FormatFloat(value, 'f', f.Decimals, 64) + " " + units[idx]
}
