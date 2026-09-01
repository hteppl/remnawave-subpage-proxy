package subinfo

import (
	"strconv"
	"strings"
)

const UserInfoHeader = "subscription-userinfo"

// UserInfo is the parsed header. Fields are -1 when absent, which a zero total
// is not: that means unlimited.
type UserInfo struct {
	Upload   int64
	Download int64
	Total    int64
	Expire   int64
}

func (u UserInfo) Used() int64 {
	switch {
	case u.Upload < 0 && u.Download < 0:
		return -1
	case u.Upload < 0:
		return u.Download
	case u.Download < 0:
		return u.Upload
	default:
		return u.Upload + u.Download
	}
}

// ParseUserInfo reads "upload=0; download=0; total=0; expire=0", reporting
// false when no field was usable. Costs no panel call.
func ParseUserInfo(header string) (UserInfo, bool) {
	info := UserInfo{Upload: -1, Download: -1, Total: -1, Expire: -1}
	if strings.TrimSpace(header) == "" {
		return info, false
	}

	any := false
	for _, part := range strings.Split(header, ";") {
		key, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "upload":
			info.Upload, any = n, true
		case "download":
			info.Download, any = n, true
		case "total":
			info.Total, any = n, true
		case "expire":
			info.Expire, any = n, true
		}
	}
	return info, any
}

// ForceUnlimitedTotal sets total=0, the encoding for an unlimited plan. Every
// other field is preserved.
func ForceUnlimitedTotal(header string) string {
	if strings.TrimSpace(header) == "" {
		return "total=0"
	}

	parts := strings.Split(header, ";")
	out := make([]string, 0, len(parts)+1)
	replaced := false

	for _, part := range parts {
		key, _, found := strings.Cut(part, "=")
		if found && strings.EqualFold(strings.TrimSpace(key), "total") {
			out = append(out, "total=0")
			replaced = true
			continue
		}
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if !replaced {
		out = append(out, "total=0")
	}
	return strings.Join(out, "; ")
}
