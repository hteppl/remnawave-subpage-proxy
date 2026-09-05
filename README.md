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

An announce is defined in the panel or in the proxy configuration:

```
Used {TRAFFIC_USED} of {TRAFFIC_LIMIT} · {DAYS_LEFT} days left
```

Every client receives it with the values resolved:

```
Used 10.50 GB of 100.00 GB · 12 days left
```

## Features

- **Template Variables** - Fill `{TRAFFIC_USED}`, `{TRAFFIC_AVAILABLE}`, `{DAYS_LEFT}` and more into subscription params
- **Zero-Cost Placeholders** - Traffic and expiry come from the response headers, so the common case makes no API call;
  username, status and lifetime traffic come from the panel, cached and de-duplicated
- **Two Template Sources** - Write the announce in the panel's Custom Response Headers or in `config.yaml`
- **Conditional Rules** - Different text per client type, user agent or user status
- **Fallback Cache** - Optionally replays the last good subscription while Remnawave is unreachable
- **Force Unlimited** - Optionally reports every plan as unlimited, whatever quota the panel holds
- **Host Shuffling** - Optionally shuffles the servers matching a name pattern, so users spread across them
- **Transparent Proxy** - Header casing, the `X-Forwarded-*` chain and drop-on-error all preserved
- **Docker Ready** - Multi-arch image, non-root, read-only, self-probing healthcheck

## Prerequisites

Docker and Docker Compose, a Remnawave panel with a subscription page, and an
API token from Remnawave Settings → API Tokens. The subscription page is bundled
in the compose file if it is not already running.

## How it works

The proxy sits in front of the official
[subscription page](https://github.com/remnawave/subscription-page) and rewrites
response headers. The page is left unmodified: the web interface, browser
detection, client-type templates, Marzban legacy links and subpage configurations
keep working, and upstream updates still apply.

```
client → caddy/nginx :443 → subpage-proxy :3020 → subscription-page :3010 → panel
                                  │
                                  └── GET /api/sub/{shortUuid}/info   (only when needed)
```

Traffic and expiry placeholders come from the `subscription-userinfo` header on
every subscription response, so the common case requires **no API request**. The
panel is queried only for data the headers cannot supply, such as `{USERNAME}`
or `{USER_STATUS}`; those lookups are cached and de-duplicated.

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

`config.yaml` is optional: without it, the proxy resolves the placeholders
defined in the panel's Custom Response Headers.

To build from source instead of the published image, run `make dev`: it applies
`docker-compose.dev.yml`, which builds the `:dev` tag locally, sets logging to
`debug`/`text` and publishes the health port on 3021.

Finally, point the reverse proxy at the proxy rather than the subscription page.
No port is published on the host — the containers live only on
`remnawave-network` — so a reverse proxy on that network addresses them by name.

```caddy
sub.example.com {
    reverse_proxy remnawave-subpage-proxy:3020
}
```

If the reverse proxy runs on the host instead, publish the port by adding
`ports: ['127.0.0.1:3020:3020']` to the service in the compose file.

### Existing subscription page deployment

Use `docker-compose.proxy-only.yml` and drop the `ports:` mapping from the
subscription page's compose file, so it is reachable only through the proxy.

```bash
cp .env.example .env
cp config.example.yaml config.yaml
docker compose -f docker-compose.proxy-only.yml up -d
```

## Production notes

A specific version should be pinned rather than tracking `latest`. Both compose
files run the container read-only, with all capabilities dropped,
`no-new-privileges` set, and the JSON log file capped at 3 × 10 MB.

`LOG_FORMAT=json` is recommended where logs are forwarded to an external system.
`HTTP_SHUTDOWN_TIMEOUT` must stay below the compose `stop_grace_period` (15s and
20s by default), so in-flight requests finish before the container is stopped.

The panel is verified once at startup; a failure is logged at error level but
does not prevent operation.

## Configuring the announce

The template may be defined in either place, and both are applied.

**From the panel** — Remnawave Settings → Subscription Settings → Custom
Response Headers:

| Header     | Value                                    |
|------------|------------------------------------------|
| `announce` | `Used {TRAFFIC_USED} of {TRAFFIC_LIMIT}` |

The proxy resolves the placeholders in the outgoing response; nothing else is
required. `scan_all_headers` is on by default, so this applies to any header the
panel sets, and base64 values are decoded, resolved and re-encoded in place.

**From `config.yaml`** — for templates kept in version control, or needing
conditions:

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

Every option is documented in [`config.example.yaml`](config.example.yaml), and
[`examples/`](examples/) holds ready-made configurations for common setups.

## Placeholders

Syntax is `{NAME}`, with optional chained modifiers: `{NAME|upper}`,
`{NAME|lower}`, `{NAME|trim}`, `{NAME|truncate:40}`, `{NAME|default:n/a}`. Names
use `UPPER_SNAKE_CASE`, so JSON and Clash payloads passing through the proxy are
never interpreted as templates.

### Resolved without a panel request

| Placeholder                 | Example                                      |
|-----------------------------|----------------------------------------------|
| `{TRAFFIC_USED}`            | `10.50 GB`                                   |
| `{TRAFFIC_LIMIT}`           | `100.00 GB`                                  |
| `{TRAFFIC_AVAILABLE}`       | `89.50 GB` (limit minus used)                |
| `{TRAFFIC_USED_BYTES}`      | `10500000000`                                |
| `{TRAFFIC_LIMIT_BYTES}`     | `100000000000`                               |
| `{TRAFFIC_USED_IN_LIMIT}`   | `3.0` (used, in the limit's unit, no suffix) |
| `{TRAFFIC_LIMIT_VALUE}`     | `20.0` (limit, no suffix)                    |
| `{TRAFFIC_UNIT}`            | `GB` (the unit both share)                   |
| `{TRAFFIC_AVAILABLE_BYTES}` | `89500000000`                                |
| `{TRAFFIC_UPLOAD}`          | `0.50 GB`                                    |
| `{TRAFFIC_DOWNLOAD}`        | `10.00 GB`                                   |
| `{TRAFFIC_USED_PERCENT}`    | `10`                                         |
| `{TRAFFIC_LEFT_PERCENT}`    | `90`                                         |
| `{PROGRESS_BAR}`            | `▰▱▱▱▱▱▱▱▱▱`                                 |
| `{DAYS_LEFT}`               | `12`                                         |
| `{EXPIRES_AT}`              | `31.12.2026 23:59`                           |
| `{EXPIRES_AT_DATE}`         | `31.12.2026`                                 |
| `{EXPIRES_AT_TIME}`         | `23:59`                                      |
| `{EXPIRES_AT_UNIX}`         | `1798761599`                                 |
| `{SHORT_UUID}`              | `aBcDeF123`                                  |
| `{CLIENT_TYPE}`             | `clash`                                      |
| `{USER_AGENT}`              | `Happ/1.0`                                   |
| `{CLIENT_IP}`               | `203.0.113.9`                                |
| `{ORIGINAL_VALUE}`          | the header's own text, before rewriting      |
| `{NOW}` `{DATE}` `{TIME}`   | `01.09.2026 14:30`                           |
| `{SUBSCRIPTION_URL}`        | `https://example.com/sub/aBcDeF123`          |

`{TRAFFIC_USED_IN_LIMIT}`, `{TRAFFIC_LIMIT_VALUE}` and `{TRAFFIC_UNIT}` render
both sides of a quota in one shared unit: `{TRAFFIC_USED_IN_LIMIT} of
{TRAFFIC_LIMIT}` gives `0.0 of 20.0 GB` where `{TRAFFIC_USED}` would give
`0 B of 20.0 GB`. The unit follows the limit.

`{ORIGINAL_VALUE}` holds the text the panel sent for the header the rule targets,
decoded if it was base64, so a `template` can wrap the panel's announce rather
than discard it; placeholders the panel used are resolved inside it.

`{SUBSCRIPTION_URL}` is rebuilt from the incoming request, so it needs no panel
request; the client-type segment is dropped, and `/{shortUuid}/clash` yields the
plain `/{shortUuid}` link.

An unlimited plan renders `{TRAFFIC_LIMIT}` and `{TRAFFIC_AVAILABLE}` as `∞`
(configurable), as does `{EXPIRES_AT}` without an expiry date.

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

Rules may be scoped so different clients or users receive different text:

```yaml
headers:
  # Happ only.
  - name: announce
    template: "{PROGRESS_BAR} {TRAFFIC_USED_PERCENT}% used"
    encode: base64-prefixed
    when:
      user_agent: "(?i)happ"

  # Client-type paths /json and /clash only; `user_statuses` works the same way.
  - name: X-Plan-Summary
    template: "{TRAFFIC_USED} / {TRAFFIC_LIMIT}"
    when:
      client_types: [ json, clash ]

  # Plans with a finite quota; `false` matches unlimited plans.
  - name: announce
    template: "{TRAFFIC_USED} of {TRAFFIC_LIMIT} used"
    encode: base64-prefixed
    when:
      has_traffic_limit: true

  # Default value, applied only if the panel did not send the header.
  - name: support-url
    template: "https://t.me/my_support_bot"
    when:
      exists: false
```

`has_traffic_limit` distinguishes a finite quota from an unlimited plan, which
Remnawave encodes as a zero total. It comes from the `subscription-userinfo`
header, so it normally costs no panel request, and a rule is skipped when the
quota cannot be determined at all.

`traffic.force_unlimited` does not affect it: that option changes what the client
is shown, while `has_traffic_limit` tests the quota configured in the panel, so a
plan presented as unlimited still matches `has_traffic_limit: true`.
`user_statuses` always triggers a panel request, as the status is absent from
the response headers.

## Configuration reference

Infrastructure is configured through environment variables, templating through
`config.yaml`. Defaults below apply when a variable is unset;
[`.env.example`](.env.example) overrides several.

| Variable                              | Default       | Meaning                                                          |
|---------------------------------------|---------------|------------------------------------------------------------------|
| `UPSTREAM_URL`                        | —             | Subscription page to proxy. **Required.**                        |
| `REMNAWAVE_PANEL_URL`                 | —             | Panel base URL, unless `PANEL_ENABLED=false`.                    |
| `REMNAWAVE_API_TOKEN`                 | —             | Panel API token, unless `PANEL_ENABLED=false`.                   |
| `APP_HOST`                            | `0.0.0.0`     | Public bind address.                                             |
| `APP_PORT`                            | `3020`        | Public port.                                                     |
| `HEALTH_HOST`                         | `0.0.0.0`     | Bind address for the health endpoints.                           |
| `HEALTH_PORT`                         | `3021`        | Health port. `0` disables both endpoints.                        |
| `CONFIG_PATH`                         | `config.yaml` | Header rules file. Missing is an error only when set explicitly. |
| `CUSTOM_SUB_PREFIX`                   | —             | Path prefix. Must match the subscription page's setting.         |
| `TRUST_PROXY`                         | `1`           | `true`/`false`, a hop count, or presets, IPs and CIDRs.          |
| `UPSTREAM_FORCE_HTTPS`                | `false`       | Always send `X-Forwarded-Proto: https` upstream.                 |
| `PANEL_ENABLED`                       | `true`        | `false` runs without panel credentials.                          |
| `PANEL_ALWAYS_FETCH`                  | `false`       | Look up every subscription, even when nothing needs it.          |
| `PANEL_FORWARD_REAL_IP`               | `false`       | Send the end user's IP on info lookups.                          |
| `PANEL_TIMEOUT`                       | `10s`         | Timeout for one panel API call.                                  |
| `CACHE_TTL`                           | `30s`         | How long a successful panel lookup is reused.                    |
| `CACHE_NEGATIVE_TTL`                  | `10s`         | How long a "not found" is remembered.                            |
| `CACHE_MAX_ENTRIES`                   | `10000`       | Cap on cached lookups.                                           |
| `CADDY_AUTH_API_TOKEN`                | —             | `X-Api-Key` for a panel behind Caddy security.                   |
| `CLOUDFLARE_ZERO_TRUST_CLIENT_ID`     | —             | `CF-Access-Client-Id` for Cloudflare Zero Trust.                 |
| `CLOUDFLARE_ZERO_TRUST_CLIENT_SECRET` | —             | `CF-Access-Client-Secret` for the same.                          |
| `SUBSCRIPTION_CACHE_ENABLED`          | `false`       | Replay the last good response while Remnawave is down.           |
| `SUBSCRIPTION_CACHE_TTL`              | `1h`          | How long a stored response stays usable.                         |
| `SUBSCRIPTION_CACHE_MAX_BYTES`        | `64MiB`       | Total memory budget for the fallback cache.                      |
| `SUBSCRIPTION_CACHE_MAX_BODY`         | `1MiB`        | Largest single response worth storing.                           |
| `UPSTREAM_TIMEOUT`                    | `60s`         | Wait for upstream response headers.                              |
| `HTTP_READ_TIMEOUT`                   | `30s`         | Reading the client request.                                      |
| `HTTP_WRITE_TIMEOUT`                  | `90s`         | Writing the response.                                            |
| `HTTP_IDLE_TIMEOUT`                   | `120s`        | Keep-alive idle time.                                            |
| `HTTP_SHUTDOWN_TIMEOUT`               | `15s`         | Drain on SIGTERM. Keep below `stop_grace_period`.                |
| `LOG_LEVEL`                           | `info`        | `debug` logs every header rewrite.                               |
| `LOG_FORMAT`                          | `text`        | `text` or `json`.                                                |

A duration without a unit means seconds, so `CACHE_TTL=45` and `CACHE_TTL=45s`
are equivalent. A byte size accepts `1MiB`, `512KB` or a plain number.

### Forcing an unlimited plan

`traffic.force_unlimited` hides the quota from the client's built-in traffic
display, whatever the panel has configured:

```yaml
traffic:
  force_unlimited: true
```

The `subscription-userinfo` header is sent with `total=0`, the standard encoding
for an unlimited plan and the value client apps read for their quota display.
Only that header is rewritten: placeholders and conditions keep reporting the
real quota, so `{TRAFFIC_LIMIT}` still yields `100.00 GB` and
`has_traffic_limit: true` still matches. It controls the one thing a template
cannot — the app's own traffic readout.

### Subscription fallback cache

Disabled by default; enabled with `SUBSCRIPTION_CACHE_ENABLED=true`. The proxy
then keeps the last successful subscription response per client and replays it
while Remnawave is unreachable, so existing users keep a working configuration
through a panel outage or a page restart.

This is a fallback, not a read-through cache: every request goes to the upstream
first, and the stored copy is used only if that fails — a dropped connection, a
timeout, or a 5xx response. Entries are keyed by short UUID, client type,
`User-Agent` and `Accept-Encoding`, since Remnawave varies the payload by
client; only subscription payloads are stored, never the web page.

Two consequences: traffic counters in a replay are as old as the cache entry,
and a user revoked during an outage keeps access until `SUBSCRIPTION_CACHE_TTL`
expires.

### Shuffling hosts

Clients tend to connect to the first host in a subscription, so a fixed order
sends every user to the same server. `hosts.shuffle` groups hosts by a Go regexp
matched against the name the client shows — the link fragment or vmess `ps`,
Xray `remarks`, the sing-box `tag`, the Clash proxy `name`. On each request a
group's hosts are shuffled among the positions they already hold, while a host
matching no pattern keeps its place:

```yaml
hosts:
  shuffle:
    - "(?i)premium"    # every Premium node is shuffled with the others
    - "^🇩🇪 "           # a second, independent group
```

Server addresses are never matched, so hosts can be regrouped by renaming them
in the panel. A host matching several groups belongs to the first, `".*"`
shuffles every host, and the list is empty by default — nothing is buffered or
rewritten until a group is set.

The format is detected from the body, whatever path or user agent produced it.
A link list, base64-wrapped or not, moves line by line; `/json` and
`/v2ray-json` move whole Xray configs; in sing-box the node outbounds are
shuffled and every selector or urltest listing them by tag follows; in Clash,
Mihomo and Stash YAML the `proxies` list is shuffled and the proxy groups
follow. `DIRECT`, `REJECT` and group names keep their positions, and the web
page is never modified.

With shuffling enabled the proxy asks the upstream for an uncompressed body on
subscription paths; one that still arrives compressed, or is larger than 8 MiB,
passes through unchanged. A replay from the fallback cache is shuffled as well.

### Blocking scanner probes

Automated sweeps for `/.env`, `/.git/HEAD`, `/config/.env` and similar paths are
refused by the proxy, so they never reach the subscription page or the panel.
They receive a bare `404`, logged only at `debug` level:

```yaml
block:
  enabled: true
  patterns:
    - "(?i)/telescope"
```

`enabled: false` turns the whole filter off, custom `patterns` included.

Built in are any path segment starting with a dot, the file types a probe asks
for (`.php`, `.sql`, `.bak`, `.ini`, `.key` and similar) in any segment — a
trailing slash does not bypass them — and first segments such as `env`,
`wp-admin` or `phpmyadmin`, weighed after `CUSTOM_SUB_PREFIX` is removed. `..`
is refused wherever it appears, including under `/assets`.

Exempt are `.well-known`, which carries ACME challenges and `security.txt`, in
any spelling, and the page's own `/assets/.app-config-v2.json` — those two only. Short UUIDs are
nanoids, so no built-in name collides with a real subscription in practice, but
nothing enforces that shape.

### Health

`/healthz` on `HEALTH_PORT` reports liveness, `/readyz` also verifies that the
upstream is reachable. Both sit on a separate port so no request path can shadow
a short UUID, and the container `HEALTHCHECK` invokes the binary against its own
endpoint, so the image needs neither a shell nor `curl`.

## Notes on behaviour

- **Header casing is preserved.** Go canonicalises header names on parsing
  (`announce` → `Announce`); the proxy restores the spelling the subscription
  page uses, so clients see the same header as before.
- **Base64 values are handled safely.** A value is decoded and re-encoded only
  when decoding reveals a placeholder; anything else passes through byte for
  byte.
- **`X-Forwarded-*` is chained, not overwritten,** so the subscription page
  resolves the real client with its own `TRUST_PROXY=1`. Requests the upstream
  refuses are dropped rather than answered, as the page itself does.
- **The panel is not on the critical path.** If it is unavailable, subscriptions
  are still served; placeholders that require it retain their literal text.
- **Marzban legacy links work partially.** Their path segment is an opaque token
  rather than a short UUID, so the user cannot be looked up: traffic and expiry
  still resolve from the response header, `{USERNAME}` and other panel-backed
  placeholders do not.

## License

This project is licensed under the [GNU General Public License v3.0](LICENSE).
