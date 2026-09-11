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

var Catalog = map[string]Origin{
	"TRAFFIC_USED":            OriginTraffic,
	"TRAFFIC_USED_BYTES":      OriginTraffic,
	"TRAFFIC_LIMIT":           OriginTraffic,
	"TRAFFIC_LIMIT_BYTES":     OriginTraffic,
	"TRAFFIC_USED_IN_LIMIT":   OriginTraffic,
	"TRAFFIC_LIMIT_VALUE":     OriginTraffic,
	"TRAFFIC_UNIT":            OriginTraffic,
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

	"ORIGINAL_VALUE":   OriginLocal,
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
	traffic  config.TrafficFormat
	datetime config.DateTimeFormat
	bar      config.ProgressBar
	custom   map[string]string

	now func() time.Time
}

func NewResolver(f config.File) *Resolver {
	custom := make(map[string]string, len(f.Vars))
	for k, v := range f.Vars {
		custom[k] = v
	}
	return &Resolver{
		traffic:  f.Traffic,
		datetime: f.DateTime,
		bar:      f.ProgressBar,
		custom:   custom,
		now:      time.Now,
	}
}

// Limit reports the traffic allowance in bytes; zero means unlimited.
func (r *Resolver) Limit(src Source) (int64, bool) {
	_, limit, ok := r.trafficCounters(src)
	return limit, ok && limit >= 0
}

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

// resolve picks the source by Catalog origin; names outside the Catalog have no value.
func (r *Resolver) resolve(name string, src Source) (string, bool) {
	origin, known := Catalog[name]
	switch {
	case !known:
		return "", false
	case origin == OriginPanel:
		return r.resolvePanel(name, src.Panel)
	case origin == OriginTraffic:
		return r.resolveTraffic(name, src)
	default:
		return r.resolveLocal(name, src)
	}
}

// ORIGINAL_VALUE is answered by the engine per header, so it has no value here.
func (r *Resolver) resolveLocal(name string, src Source) (string, bool) {
	switch name {
	case "SHORT_UUID":
		return nonEmpty(src.ShortUUID)
	case "CLIENT_TYPE":
		return nonEmpty(src.ClientType)
	case "USER_AGENT":
		return nonEmpty(src.UserAgent)
	case "CLIENT_IP":
		return nonEmpty(src.ClientIP)
	case "SUBSCRIPTION_URL":
		return nonEmpty(src.SubscriptionURL)
	case "NOW":
		return r.format(r.now(), r.fullLayout()), true
	case "DATE":
		return r.format(r.now(), r.datetime.Layout), true
	case "TIME":
		return r.format(r.now(), r.datetime.TimeLayout), true
	}
	return "", false
}

func (r *Resolver) resolvePanel(name string, info *panel.Info) (string, bool) {
	if info == nil {
		return "", false
	}
	u := info.User
	switch name {
	case "USERNAME":
		return nonEmpty(u.Username)
	case "USER_STATUS":
		return nonEmpty(u.UserStatus)
	case "IS_ACTIVE":
		return strconv.FormatBool(u.IsActive), true
	case "TRAFFIC_LIMIT_STRATEGY":
		return nonEmpty(u.TrafficLimitStrategy)
	case "LIFETIME_TRAFFIC_USED":
		return r.bytes(u.LifetimeUsedBytes())
	}
	return "", false
}

func (r *Resolver) resolveTraffic(name string, src Source) (string, bool) {
	used, limit, ok := r.trafficCounters(src)
	if !ok {
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
	case "TRAFFIC_USED_IN_LIMIT":
		if used < 0 {
			return "", false
		}
		_, base := unitsOf(r.traffic)
		return formatScaled(used, r.limitScale(used, limit), base, r.traffic.Decimals), true
	case "TRAFFIC_LIMIT_VALUE":
		if limit < 0 {
			return "", false
		}
		if limit == 0 {
			return r.traffic.Unlimited, true
		}
		_, base := unitsOf(r.traffic)
		return formatScaled(limit, r.limitScale(used, limit), base, r.traffic.Decimals), true
	case "TRAFFIC_UNIT":
		if limit < 0 && used < 0 {
			return "", false
		}
		units, _ := unitsOf(r.traffic)
		return units[r.limitScale(used, limit)], true
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
	case "TRAFFIC_USED_PERCENT", "TRAFFIC_LEFT_PERCENT", "PROGRESS_BAR":
		pct, ok := percentUsed(used, limit)
		switch {
		case !ok:
			return "", false
		case name == "TRAFFIC_USED_PERCENT":
			return strconv.Itoa(pct), true
		case name == "TRAFFIC_LEFT_PERCENT":
			return strconv.Itoa(100 - pct), true
		default:
			return r.progressBar(pct), true
		}
	case "TRAFFIC_UPLOAD", "TRAFFIC_DOWNLOAD":
		switch {
		case src.UserInfo == nil:
			return "", false
		case name == "TRAFFIC_UPLOAD":
			return r.bytes(src.UserInfo.Upload)
		default:
			return r.bytes(src.UserInfo.Download)
		}
	}
	return r.resolveExpiry(name, src)
}

func (r *Resolver) resolveExpiry(name string, src Source) (string, bool) {
	expiry, hasExpiry := r.expiry(src)
	var layout string
	switch name {
	case "DAYS_LEFT":
		if src.Panel != nil {
			return strconv.Itoa(max(0, src.Panel.User.DaysLeft)), true
		}
		if !hasExpiry {
			return "", false
		}
		return strconv.Itoa(daysUntil(r.now(), expiry)), true
	case "EXPIRES_AT_UNIX":
		if !hasExpiry {
			return "0", true
		}
		return strconv.FormatInt(expiry.Unix(), 10), true
	case "EXPIRES_AT":
		layout = r.fullLayout()
	case "EXPIRES_AT_DATE":
		layout = r.datetime.Layout
	case "EXPIRES_AT_TIME":
		layout = r.datetime.TimeLayout
	default:
		return "", false
	}
	if !hasExpiry {
		return r.datetime.Never, true
	}
	return r.format(expiry, layout), true
}

func (r *Resolver) format(t time.Time, layout string) string {
	return t.In(r.datetime.Location()).Format(layout)
}

func (r *Resolver) fullLayout() string {
	return r.datetime.Layout + " " + r.datetime.TimeLayout
}

func nonEmpty(s string) (string, bool) {
	return s, s != ""
}

// trafficCounters prefers the response header: it matches what the client received and is free.
func (r *Resolver) trafficCounters(src Source) (used, limit int64, ok bool) {
	used, limit = -1, -1

	if src.UserInfo != nil {
		u, l := src.UserInfo.Used(), src.UserInfo.Total
		if u >= 0 || l >= 0 {
			used, limit, ok = u, l, true
		}
	}
	if src.Panel != nil {
		// Fill whatever the header left out rather than discarding the panel.
		if used < 0 {
			used = src.Panel.User.UsedBytes()
		}
		if limit < 0 {
			limit = src.Panel.User.LimitBytes()
		}
		ok = ok || used >= 0 || limit >= 0
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
	units, base := unitsOf(f)
	idx := unitIndex(n, base, len(units)-1)
	return formatScaled(n, idx, base, f.Decimals) + " " + units[idx]
}

func unitsOf(f config.TrafficFormat) ([]string, int64) {
	units, base := decimalUnits, int64(1000)
	if f.BinaryUnits {
		units, base = binaryUnits, 1024
	}
	if len(f.Units) > 0 {
		units = f.Units
	}
	return units, base
}

func unitIndex(n, base int64, maxIdx int) int {
	value, idx := float64(n), 0
	for value >= float64(base) && idx < maxIdx {
		value /= float64(base)
		idx++
	}
	return idx
}

func formatScaled(n int64, idx int, base int64, decimals int) string {
	if idx == 0 {
		return strconv.FormatInt(n, 10)
	}
	return strconv.FormatFloat(float64(n)/math.Pow(float64(base), float64(idx)), 'f', decimals, 64)
}

// limitScale is the unit shared by both sides of "X of Y", following the limit when there is one.
func (r *Resolver) limitScale(used, limit int64) int {
	units, base := unitsOf(r.traffic)
	n := limit
	if n <= 0 {
		n = used
	}
	if n < 0 {
		n = 0
	}
	return unitIndex(n, base, len(units)-1)
}
