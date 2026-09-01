package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hteppl/remnawave-subpage-proxy/internal/version"
)

var ErrNotFound = errors.New("subscription not found")

// shortUUIDPattern guards a value that arrives from the request path on calls
// carrying the panel API token: url.JoinPath resolves ".." instead of escaping
// it, which would walk out of /api/sub/ as an authenticated caller.
var shortUUIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// realIPHeader matches REMNAWAVE_REAL_IP_HEADER in @remnawave/backend-contract.
const realIPHeader = "x-remnawave-real-ip"

// User mirrors response.user of GET /api/sub/{shortUuid}/info. Byte counters
// are strings because they can exceed 2^53 in JSON.
type User struct {
	ShortUUID                string `json:"shortUuid"`
	Username                 string `json:"username"`
	DaysLeft                 int    `json:"daysLeft"`
	TrafficUsed              string `json:"trafficUsed"`
	TrafficLimit             string `json:"trafficLimit"`
	LifetimeTrafficUsed      string `json:"lifetimeTrafficUsed"`
	TrafficUsedBytes         string `json:"trafficUsedBytes"`
	TrafficLimitBytes        string `json:"trafficLimitBytes"`
	LifetimeTrafficUsedBytes string `json:"lifetimeTrafficUsedBytes"`
	ExpiresAt                string `json:"expiresAt"`
	IsActive                 bool   `json:"isActive"`
	UserStatus               string `json:"userStatus"`
	TrafficLimitStrategy     string `json:"trafficLimitStrategy"`
}

type Info struct {
	IsFound         bool   `json:"isFound"`
	User            User   `json:"user"`
	SubscriptionURL string `json:"subscriptionUrl"`
}

func (u User) UsedBytes() int64 { return parseBytes(u.TrafficUsedBytes) }

// LimitBytes returns -1 when unparsable; zero means unlimited.
func (u User) LimitBytes() int64 { return parseBytes(u.TrafficLimitBytes) }

func (u User) LifetimeUsedBytes() int64 { return parseBytes(u.LifetimeTrafficUsedBytes) }

func (u User) Expiry() (time.Time, bool) {
	if u.ExpiresAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, u.ExpiresAt)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func parseBytes(s string) int64 {
	if s == "" {
		return -1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

type Options struct {
	BaseURL          *url.URL
	Token            string
	Timeout          time.Duration
	CaddyAuthToken   string
	CloudflareID     string
	CloudflareSecret string
}

// Client is a minimal, concurrency-safe Remnawave panel API client.
type Client struct {
	base   *url.URL
	http   *http.Client
	common http.Header
}

func New(opts Options) *Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}

	common := http.Header{}
	common.Set("Authorization", "Bearer "+opts.Token)
	common.Set("User-Agent", version.UserAgent())
	common.Set("Accept", "application/json")
	common.Set("X-Subpage-Proxy-Version", version.Version)

	if opts.CaddyAuthToken != "" {
		common.Set("X-Api-Key", opts.CaddyAuthToken)
	}
	if opts.CloudflareID != "" && opts.CloudflareSecret != "" {
		common.Set("CF-Access-Client-Id", opts.CloudflareID)
		common.Set("CF-Access-Client-Secret", opts.CloudflareSecret)
	}
	// The panel refuses some routes on plain HTTP unless it believes it sits
	// behind a TLS-terminating hop.
	if opts.BaseURL != nil && opts.BaseURL.Scheme == "http" {
		common.Set("X-Forwarded-Proto", "https")
		common.Set("X-Forwarded-For", "127.0.0.1")
	}

	return &Client{
		base:   opts.BaseURL,
		http:   &http.Client{Transport: transport, Timeout: opts.Timeout},
		common: common,
	}
}

// SubscriptionInfo fetches GET /api/sub/{shortUuid}/info. An empty realIP keeps
// the lookup out of the panel's request history.
func (c *Client) SubscriptionInfo(ctx context.Context, shortUUID, realIP string) (*Info, error) {
	if !shortUUIDPattern.MatchString(shortUUID) {
		return nil, ErrNotFound
	}

	endpoint := *c.base
	endpoint.Path = strings.TrimSuffix(c.base.Path, "/") + "/api/sub/" + url.PathEscape(shortUUID) + "/info"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build info request: %w", err)
	}
	req.Header = c.common.Clone()
	if realIP != "" {
		req.Header.Set(realIPHeader, realIP)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("info request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("panel rejected the API token (HTTP %d); check REMNAWAVE_API_TOKEN", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("panel returned HTTP %d", resp.StatusCode)
	}

	var envelope struct {
		Response Info `json:"response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode info response: %w", err)
	}
	if !envelope.Response.IsFound {
		return nil, ErrNotFound
	}
	return &envelope.Response, nil
}

// Ping verifies panel reachability and credentials at startup.
func (c *Client) Ping(ctx context.Context) (string, error) {
	endpoint := c.base.JoinPath("api", "system", "metadata")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header = c.common.Clone()

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var envelope struct {
		Response struct {
			Version string `json:"version"`
		} `json:"response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return "", err
	}
	if v := strings.TrimSpace(envelope.Response.Version); v != "" {
		return v, nil
	}
	return "unknown", nil
}
