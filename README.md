<p align="center">
  <img src="https://raw.githubusercontent.com/hteppl/remnawave-subpage-proxy/master/.github/images/logo.webp" alt="remnawave-subpage-proxy" width="800px">
</p>

## remnawave-subpage-proxy

[![Release](https://img.shields.io/github/v/release/hteppl/remnawave-subpage-proxy?logo=github&logoColor=white&label=release)](https://github.com/hteppl/remnawave-subpage-proxy/releases/latest)
[![Docker Image](https://img.shields.io/docker/v/hteppl/remnawave-subpage-proxy?logo=docker&logoColor=white&label=docker)](https://hub.docker.com/r/hteppl/remnawave-subpage-proxy)
[![Build](https://img.shields.io/github/actions/workflow/status/hteppl/remnawave-subpage-proxy/dockerhub-publish.yaml?logo=githubactions&logoColor=white&label=build)](https://github.com/hteppl/remnawave-subpage-proxy/actions/workflows/dockerhub-publish.yaml)
[![Go](https://img.shields.io/badge/go-1.27-blue.svg?logo=go&logoColor=white)](https://github.com/hteppl/remnawave-subpage-proxy/blob/master/go.mod)
[![License: GPL v3](https://img.shields.io/badge/license-GPL--3.0-blue.svg)](https://github.com/hteppl/remnawave-subpage-proxy/blob/master/LICENSE)

English | [Русский](README.ru.md)

Automatically fill variables in Remnawave (https://docs.rw) subscription params.

An announce is defined in the Remnawave panel or in the proxy configuration:

```
Used {TRAFFIC_USED} of {TRAFFIC_LIMIT} · {DAYS_LEFT} days left
```

Every client receives it with the values resolved:

```
Used 10.50 GB of 100.00 GB · 12 days left
```

## Features

- **Template Variables** - Fill `{TRAFFIC_USED}`, `{TRAFFIC_AVAILABLE}`, `{DAYS_LEFT}` and more into subscription params
- **Zero-Cost Placeholders** - Traffic and expiry come from the response headers, so the common case makes no extra API
  call
- **Panel-Backed Data** - Username, status and lifetime traffic from the Remnawave API, cached and de-duplicated
- **Two Template Sources** - Write the announce in the panel's Custom Response Headers or in `config.yaml`
- **Conditional Rules** - Different text per client type, user agent or user status
- **Fallback Cache** - Optionally replays the last good subscription while Remnawave is unreachable
- **Force Unlimited** - Optionally reports every plan as unlimited, whatever quota the panel holds
- **Transparent Proxy** - Header casing, the `X-Forwarded-*` chain and drop-on-error all preserved
- **Docker Ready** - Multi-arch image, non-root, read-only, self-probing healthcheck

## Prerequisites

Before you begin, ensure you have the following:

- **Remnawave Panel** with a subscription page configured
- **Remnawave API Token** - Generate in Remnawave Settings → API Tokens
- **remnawave/subscription-page** - Bundled in the compose file, or already running
- **Docker and Docker Compose**

## How it works

The proxy sits in front of the official
[subscription page](https://github.com/remnawave/subscription-page) and rewrites
response headers. The page itself is left unmodified: the web interface, browser
detection, client-type templates, Marzban legacy links and subpage configurations
continue to operate, and upstream updates remain applicable.

```
client → caddy/nginx :443 → subpage-proxy :3020 → subscription-page :3010 → panel
                                  │
                                  └── GET /api/sub/{shortUuid}/info   (only when needed)
```

Traffic and expiry placeholders are resolved from the `subscription-userinfo`
header that accompanies every subscription response, so the common case requires
**no additional API request**. The panel is queried only when a template
references data the headers cannot supply, such as `{USERNAME}` or
`{USER_STATUS}`; those lookups are cached and de-duplicated.

If the panel is unreachable, unresolved placeholders retain their literal text
rather than being cleared, and the subscription is delivered unmodified.

## Quick start

```bash
git clone https://github.com/hteppl/remnawave-subpage-proxy.git
cd remnawave-subpage-proxy

cp .env.example .env
cp .env.subscription-page.example .env.subscription-page
cp config.example.yaml config.yaml

# Fill in REMNAWAVE_API_TOKEN in both .env files.
# Create the token in Remnawave Dashboard → Remnawave Settings → API Tokens.

docker compose pull
docker compose up -d
```

`config.yaml` is optional. Without it, the proxy resolves placeholders defined
in the panel's Custom Response Headers.

To build from source instead of using the published image, apply the development
override:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
```

The override builds the `:dev` tag locally, sets logging to `debug`/`text` and
publishes the health port on 3021. The equivalent shorthand is `make dev`.

Finally, redirect the reverse proxy to `127.0.0.1:3020` instead of
`127.0.0.1:3010`:

```caddy
sub.example.com {
    reverse_proxy 127.0.0.1:3020
}
```

### Existing subscription page deployment

Use `docker-compose.proxy-only.yml` and remove the `ports:` mapping from the
subscription page's own compose file, so that it is reachable only through the
proxy.

```bash
cp .env.example .env
cp config.example.yaml config.yaml
docker compose -f docker-compose.proxy-only.yml up -d
```

## Production notes

A specific version should be pinned rather than tracking `latest`, by editing
the image tag in the compose file. Both compose files run the container with a
read-only filesystem, all capabilities dropped, `no-new-privileges` set, and the
JSON log file capped at 3 × 10 MB.

`LOG_FORMAT=json` is recommended where logs are forwarded to an external system.
`HTTP_SHUTDOWN_TIMEOUT` must remain below the compose `stop_grace_period`
(15s and 20s respectively by default), so that in-flight subscription requests
complete before the container is terminated.

The panel is verified once at startup. A failure is logged at error level but
does not prevent operation, as subscriptions are served without it.

## Configuring the announce

The template may be defined in either of two locations, and both are applied.

**From the panel** — Remnawave Settings → Subscription Settings → Custom
Response Headers:

| Header     | Value                                    |
|------------|------------------------------------------|
| `announce` | `Used {TRAFFIC_USED} of {TRAFFIC_LIMIT}` |

The proxy resolves the placeholders in the outgoing response. No further
configuration is required: `scan_all_headers` is enabled by default, so the
behaviour applies to any header set by the panel, not only `announce`. Values
that the panel base64-encodes are decoded, resolved and re-encoded in the same
form.

**From `config.yaml`** — for templates maintained in version control, or those
requiring conditions:

```yaml
vars:
  BRAND: "MyProject"
  SUPPORT: "@my_support_bot"

headers:
  - name: announce
    template: "{BRAND} · {TRAFFIC_USED} of {TRAFFIC_LIMIT} used · {DAYS_LEFT} days left · {SUPPORT}"
    encode: base64-prefixed   # required by Happ
    max_length: 200           # Happ displays at most 200 characters

  # Alternative message once the plan is exhausted.
  - name: profile-title
    template: "{BRAND} — {TRAFFIC_AVAILABLE}"
    encode: base64
    max_length: 25
```

All available options are documented in
[`config.example.yaml`](config.example.yaml), and [`examples/`](examples/) holds
ready-made configurations for common setups.

## Placeholders

Syntax is `{NAME}`, with optional chained modifiers: `{NAME|upper}`,
`{NAME|lower}`, `{NAME|trim}`, `{NAME|truncate:40}`, `{NAME|default:n/a}`.

Placeholder names use `UPPER_SNAKE_CASE`. JSON and Clash payloads passing
through the proxy are therefore never interpreted as templates.

### Resolved without a panel request

| Placeholder                 | Example                             |
|-----------------------------|-------------------------------------|
| `{TRAFFIC_USED}`            | `10.50 GB`                          |
| `{TRAFFIC_LIMIT}`           | `100.00 GB`                         |
| `{TRAFFIC_AVAILABLE}`       | `89.50 GB` (limit minus used)       |
| `{TRAFFIC_USED_BYTES}`      | `10500000000`                       |
| `{TRAFFIC_LIMIT_BYTES}`     | `100000000000`                      |
| `{TRAFFIC_AVAILABLE_BYTES}` | `89500000000`                       |
| `{TRAFFIC_UPLOAD}`          | `0.50 GB`                           |
| `{TRAFFIC_DOWNLOAD}`        | `10.00 GB`                          |
| `{TRAFFIC_USED_PERCENT}`    | `10`                                |
| `{TRAFFIC_LEFT_PERCENT}`    | `90`                                |
| `{PROGRESS_BAR}`            | `▰▱▱▱▱▱▱▱▱▱`                        |
| `{DAYS_LEFT}`               | `12`                                |
| `{EXPIRES_AT}`              | `31.12.2026 23:59`                  |
| `{EXPIRES_AT_DATE}`         | `31.12.2026`                        |
| `{EXPIRES_AT_TIME}`         | `23:59`                             |
| `{EXPIRES_AT_UNIX}`         | `1798761599`                        |
| `{SHORT_UUID}`              | `aBcDeF123`                         |
| `{CLIENT_TYPE}`             | `clash`                             |
| `{USER_AGENT}`              | `Happ/1.0`                          |
| `{CLIENT_IP}`               | `203.0.113.9`                       |
| `{NOW}` `{DATE}` `{TIME}`   | `01.09.2026 14:30`                  |
| `{SUBSCRIPTION_URL}`        | `https://example.com/sub/aBcDeF123` |

`{SUBSCRIPTION_URL}` is reconstructed from the incoming request — the forwarded
scheme and host plus the short UUID — so it reflects the address used by the
client and requires no panel request. The client-type segment is omitted:
`/{shortUuid}/clash` yields the plain `/{shortUuid}` link.

On an unlimited plan, `{TRAFFIC_LIMIT}` and `{TRAFFIC_AVAILABLE}` render as `∞`
(configurable); a subscription without an expiry date renders `{EXPIRES_AT}`
identically.

### Requiring a panel request (cached)

| Placeholder                | Example                                         |
|----------------------------|-------------------------------------------------|
| `{USERNAME}`               | `alice`                                         |
| `{USER_STATUS}`            | `ACTIVE` `DISABLED` `LIMITED` `EXPIRED`         |
| `{IS_ACTIVE}`              | `true`                                          |
| `{TRAFFIC_LIMIT_STRATEGY}` | `NO_RESET` `DAY` `WEEK` `MONTH` `MONTH_ROLLING` |
| `{LIFETIME_TRAFFIC_USED}`  | `1.20 TB`                                       |

In addition, any variable defined under `vars:` in `config.yaml`.

## Conditions

Rules may be scoped so that different clients or users receive different text:

```yaml
headers:
  # Happ only.
  - name: announce
    template: "{PROGRESS_BAR} {TRAFFIC_USED_PERCENT}% used"
    encode: base64-prefixed
    when:
      user_agent: "(?i)happ"

  # Client-type paths /json and /clash only.
  - name: X-Plan-Summary
    template: "{TRAFFIC_USED} / {TRAFFIC_LIMIT}"
    when:
      client_types: [ json, clash ]

  # Users who have exhausted their traffic limit.
  - name: announce
    template: "Traffic limit reached — renew at {SUPPORT}"
    encode: base64-prefixed
    when:
      user_statuses: [ LIMITED, EXPIRED ]

  # Plans with a finite quota.
  - name: announce
    template: "{TRAFFIC_USED} of {TRAFFIC_LIMIT} used"
    encode: base64-prefixed
    when:
      has_traffic_limit: true

  # Unlimited plans.
  - name: x-plan
    template: "Unlimited traffic"
    when:
      has_traffic_limit: false

  # Default value, applied only if the panel did not send the header.
  - name: support-url
    template: "https://t.me/my_support_bot"
    when:
      exists: false
```

`has_traffic_limit` distinguishes a finite quota from an unlimited plan, which
Remnawave encodes as a zero total. It is read from the `subscription-userinfo`
header and therefore normally costs no panel request; the panel is consulted
only when that header carries no total. A rule is skipped when the quota cannot
be determined at all.

`traffic.force_unlimited` does not affect this condition. It changes what the
client is shown, while `has_traffic_limit` tests the quota actually configured in
the panel, so the two can be combined: a limited plan can be presented as
unlimited and still match `has_traffic_limit: true`.

`user_statuses` always triggers a panel request, as the status is not present in
the response headers.

## Configuration reference

Infrastructure is configured through environment variables, templating through
`config.yaml`. The table below lists every variable with the default applied when
it is unset; the supplied [`.env.example`](.env.example) overrides several of
them.

| Variable                              | Default       | Meaning                                                                 |
|---------------------------------------|---------------|-------------------------------------------------------------------------|
| `UPSTREAM_URL`                        | —             | Subscription page to proxy. **Required.**                               |
| `REMNAWAVE_PANEL_URL`                 | —             | Panel base URL. Required unless `PANEL_ENABLED=false`.                  |
| `REMNAWAVE_API_TOKEN`                 | —             | Panel API token. Required unless `PANEL_ENABLED=false`.                 |
| `APP_HOST`                            | `0.0.0.0`     | Public bind address.                                                    |
| `APP_PORT`                            | `3020`        | Public port.                                                            |
| `HEALTH_HOST`                         | `0.0.0.0`     | Bind address for the health endpoints.                                  |
| `HEALTH_PORT`                         | `3021`        | Health port. `0` disables both endpoints.                               |
| `CONFIG_PATH`                         | `config.yaml` | Header rules file. A missing file is an error only when set explicitly. |
| `CUSTOM_SUB_PREFIX`                   | —             | Path prefix. Must match the subscription page's own setting.            |
| `TRUST_PROXY`                         | `1`           | `true`/`false`, a hop count, or a list of presets, IPs and CIDRs.       |
| `UPSTREAM_FORCE_HTTPS`                | `false`       | Always send `X-Forwarded-Proto: https` upstream.                        |
| `PANEL_ENABLED`                       | `true`        | `false` runs without panel credentials.                                 |
| `PANEL_ALWAYS_FETCH`                  | `false`       | Look up every subscription, even when no placeholder needs it.          |
| `PANEL_FORWARD_REAL_IP`               | `false`       | Send the end user's IP on info lookups.                                 |
| `PANEL_TIMEOUT`                       | `10s`         | Timeout for one panel API call.                                         |
| `CACHE_TTL`                           | `30s`         | How long a successful panel lookup is reused.                           |
| `CACHE_NEGATIVE_TTL`                  | `10s`         | How long a "not found" is remembered.                                   |
| `CACHE_MAX_ENTRIES`                   | `10000`       | Cap on cached lookups.                                                  |
| `CADDY_AUTH_API_TOKEN`                | —             | Sent as `X-Api-Key` for a panel behind Caddy security.                  |
| `CLOUDFLARE_ZERO_TRUST_CLIENT_ID`     | —             | `CF-Access-Client-Id` for a panel behind Cloudflare Zero Trust.         |
| `CLOUDFLARE_ZERO_TRUST_CLIENT_SECRET` | —             | `CF-Access-Client-Secret` for the same.                                 |
| `SUBSCRIPTION_CACHE_ENABLED`          | `false`       | Replay the last good response while Remnawave is down.                  |
| `SUBSCRIPTION_CACHE_TTL`              | `1h`          | How long a stored response stays usable.                                |
| `SUBSCRIPTION_CACHE_MAX_BYTES`        | `64MiB`       | Total memory budget for the fallback cache.                             |
| `SUBSCRIPTION_CACHE_MAX_BODY`         | `1MiB`        | Largest single response worth storing.                                  |
| `UPSTREAM_TIMEOUT`                    | `60s`         | Wait for the upstream's response headers.                               |
| `HTTP_READ_TIMEOUT`                   | `30s`         | Reading the client request.                                             |
| `HTTP_WRITE_TIMEOUT`                  | `90s`         | Writing the response.                                                   |
| `HTTP_IDLE_TIMEOUT`                   | `120s`        | Keep-alive idle time.                                                   |
| `HTTP_SHUTDOWN_TIMEOUT`               | `15s`         | Drain time on SIGTERM. Keep below the compose `stop_grace_period`.      |
| `LOG_LEVEL`                           | `info`        | `debug` logs every header rewrite.                                      |
| `LOG_FORMAT`                          | `text`        | `text` or `json`.                                                       |

A duration without a unit is interpreted as seconds, so `CACHE_TTL=45` and
`CACHE_TTL=45s` are equivalent. A byte size accepts `1MiB`, `512KB` or a plain
number.

### Forcing an unlimited plan

`traffic.force_unlimited` reports every subscription as unlimited to the client,
regardless of the quota configured in the panel:

```yaml
traffic:
  force_unlimited: true
```

The `subscription-userinfo` header is sent with `total=0`, the standard encoding
for an unlimited plan and the value that determines the quota display in client
applications. `{TRAFFIC_LIMIT}` and `{TRAFFIC_AVAILABLE}` then render as `∞`,
`{TRAFFIC_USED_PERCENT}` as `0`, and `{PROGRESS_BAR}` remains empty.

Consumed traffic and expiry are unaffected: `{TRAFFIC_USED}` continues to report
actual consumption, and the subscription still expires on schedule. Only the
limit is concealed.

### Subscription fallback cache

Disabled by default; enabled with `SUBSCRIPTION_CACHE_ENABLED=true`. The proxy
then retains the last successful subscription response per client and replays it
while Remnawave is unreachable, allowing existing users to retain a working
configuration throughout a panel outage or a page restart.

This is a fallback rather than a read-through cache: every request is sent to
the upstream first, and the stored copy is used only if that request fails — a
dropped connection, a timeout, or a 5xx response. No stale data is served while
Remnawave is operating normally.

Entries are keyed by short UUID, client type, `User-Agent` and `Accept-Encoding`,
as Remnawave varies the payload by client. The stored object is the completed
response, so a replayed announce carries the values held at the time of caching.
The web page is never stored, only subscription payloads.

Two consequences should be considered: traffic counters in a replayed response
are as old as the cache entry, and a user revoked during an outage retains access
until the TTL expires. `SUBSCRIPTION_CACHE_TTL` should be selected accordingly.

### Health

`/healthz` on `HEALTH_PORT` reports liveness; `/readyz` additionally verifies
that the upstream is reachable. Both are served on a separate port so that no
request path can shadow a subscription short UUID. The container `HEALTHCHECK`
invokes the binary against its own endpoint, so the image requires neither a
shell nor `curl`.

## Notes on behaviour

- **Header casing is preserved.** Go canonicalises header names when it parses a
  response (`announce` → `Announce`). The proxy restores the lowercase spelling
  the subscription page uses before writing the response, so that clients receive
  the same header spelling as before the proxy was introduced.
- **Base64 values are handled safely.** A value is only decoded and re-encoded
  when decoding reveals a placeholder. An opaque base64 payload, or plain text
  that merely happens to be valid base64, is passed through byte for byte.
- **Requests the upstream refuses are dropped, not answered.** The subscription
  page destroys the socket for invalid requests, providing no information to
  scanners; the proxy mirrors this and logs the reason at `warn` level.
- **`X-Forwarded-*` is chained, not overwritten.** The inbound chain is kept and
  this hop is appended, so the subscription page resolves the real client with
  its own `TRUST_PROXY=1`.
- **The panel is not on the critical path.** If it is unavailable, subscriptions
  are still served; placeholders that require it retain their literal text.
- **Marzban legacy links work partially.** Their path segment is an opaque token
  rather than a short UUID, so the proxy cannot look the user up in the panel.
  Traffic and expiry placeholders still resolve, as they originate from the
  response header; `{USERNAME}` and other panel-backed placeholders do not.

## License

This project is licensed under the [GNU General Public License v3.0](LICENSE).
