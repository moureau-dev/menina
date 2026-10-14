# Menina proxy

Menina's reverse proxy is the single entry point for every public hostname. It
terminates TLS (optionally), routes by `Host` + path prefix to the right
Kubernetes Service, streams responses, and hot-reloads its routing table without
restarts. It is a plain Go `net/http` server built on `httputil.ReverseProxy` —
no Lua, no config-language runtime, no dynamic code evaluation.

See [KUBERNETES.md](KUBERNETES.md) for the cluster side (how routes are produced
from Service ClusterIPs) and the [CLI reference](CLI.md) for commands.

Run it with `menina proxy`. In production it is a systemd service (installed by
`menina deploy`) so it starts with the host.

## How it works

```
client ──TLS──▶ menina proxy ──http──▶ Kubernetes Service (ClusterIP:port)
                     ▲
                     │ routing snapshots (control socket / events stream)
              menina serve (control plane)
                     ▲
               config files + reconciliation
```

1. **The control plane builds a snapshot.** On every reconciliation (and config
   change) `menina serve` resolves projects/environments/apps, looks up each
   exposed app's Service `ClusterIP`, and produces a `proxy.Snapshot` (version,
   timestamp, list of routes). See `internal/platform/reconciliation.go`.
2. **The proxy subscribes** over the control socket (initial `Snapshot()` then a
   `Stream()`), reconnecting with backoff if the control plane restarts
   (`internal/cli/proxy.go`).
3. **Snapshots are validated and swapped atomically.** `RoutingTable.Swap`
   rejects a snapshot with an empty host, an unparsable backend, or a duplicate
   `host+pathPrefix`, then stores it behind an `atomic.Pointer`. In-flight
   requests keep using the old table; new requests see the new one immediately.
   There is no request-path lock (`internal/proxy/routes.go`).
4. **Lookup** lowercases the host, strips any port, matches the host bucket, and
   picks the **longest** matching `pathPrefix` (routes are sorted by prefix
   length descending). Hosts are matched exactly — there are no wildcard hosts.
5. **Forwarding** rewrites the URL to `http://<ClusterIP>:<port>` while
   preserving the original `Host` header, adds the forwarding headers (below),
   and reverse-proxies with `FlushInterval = -1` so bodies stream as produced.

### Route sources

- **Exposed apps**: for each enabled app in each environment whose
  `exposure.enabled` is true, one route per `exposure.hosts` entry. Requires a
  `service` workload with a non-zero `workload.port` and a Service that has a
  `ClusterIP`. Missing ClusterIP ⇒ the route is skipped and a warning logged
  (the route "degrades" rather than pointing at nothing).
- **The registry**: when `registry.enabled`, `registry.domain` is auto-routed to
  the Zot Service under a synthetic `_registry` / `_platform` identity.

## TLS

Termination is controlled by `tls.terminate` (default true) and `tls.mode`:

| Mode | Behavior |
| --- | --- |
| `acme` (default) | Certificates issued/renewed via ACME (autocert). Cached on disk under `tls.acme.storageDir` (default `<tls.certDir>/acme`). |
| `manual` | Loads `<name>.crt`/`<name>.pem` + `<name>.key` from `tls.certDir` (dots stored as underscores in the filename). Re-read from disk every 60s, so renewals are picked up without a restart. |
| `none` | No termination — HTTP only (put the proxy behind something else). |
| `cert-manager` | **Not** supported for host-level termination; validation rejects it. Run cert-manager elsewhere and use `manual`, or use `acme`. |

- **Minimum TLS 1.2**, ALPN `h2` + `http/1.1`.
- **HTTP→HTTPS redirect** when `tls.httpRedirect` (or per-route
  `exposure.redirectHttp`). The proxy trusts `X-Forwarded-Proto` only from a
  configured trusted proxy, so a spoofed header cannot upgrade-loop.
- **ACME host policy**: a certificate is only issued for hosts under
  `networking.baseDomains` **or** for any host that currently has a route — so
  exposing an app is sufficient to get it TLS. Other names are refused.
- **Certificate warmup**: the proxy proactively fetches/refreshes certs for all
  routed hosts at startup, on every snapshot change, and every 30 minutes, so a
  newly exposed app does not fail its first handshake. Failures are retried
  hourly (`warmRoutes`).
- The ACME HTTP-01 challenge path (`/.well-known/acme-challenge/...`) is served
  by autocert on the HTTP listener.

## Networking modes

`networking.mode`:

| Mode | Client → public TLS | Origin | Proxy → backend |
| --- | --- | --- | --- |
| Public VPS ingress (Menina terminates TLS) | Menina proxy `:443` (ACME) | — | plain HTTP to `ClusterIP:port` |
| Public VPS ingress (TLS upstream) | Cloudflare / load balancer | proxy `:80` | plain HTTP to `ClusterIP:port` |
| Cloudflare Tunnel | Cloudflare | `cloudflared` → local origin (`http://127.0.0.1:80` by default) | plain HTTP to `ClusterIP:port` |

- `public` (default): the host is directly reachable; point DNS at
  `networking.publicIp` and let the proxy terminate TLS.
- `cloudflare-tunnel`: a Cloudflare Tunnel fronts the host. The proxy still
  serves HTTP; DNS/edge are handled by Cloudflare (`networking.cloudflare.*`).
  Menina installs the local proxy origin because the tunnel needs an origin
  service to forward to; `cloudflared` itself is installed and configured by the
  operator.

In both cases routing, headers, and streaming behave identically.

## Caching

Be clear about what is and isn't cached:

- **No HTTP response cache.** Menina does **not** cache responses, pages, or
  objects, and is not a CDN. Every request goes to the upstream app. This is
  deliberate: it keeps deploys instantly consistent (a new image is live the
  moment the Service behind the route changes) and avoids stale/auth-leak bugs.
  If you need edge caching, put a CDN in front (`cloudflare-tunnel` mode) or
  cache inside the app.
- **Upstream connection reuse.** Transports are pooled and cached **per route
  configuration** (`h2c`, read timeout, idle timeout), so keep-alive
  connections are reused rather than re-dialed. HTTP transport: up to 256 idle
  conns total, 64 per host.
- **ACME certificate cache** on disk (see above), plus an in-memory scheduling
  map of the next warmup per host.
- **In-memory routing table** — the current snapshot, replaced atomically.
- **On-the-fly gzip** (`exposure.compression`, default off) compresses response
  bodies when the client sends `Accept-Encoding: gzip`. It is computed per
  response, not cached; it also strips an upstream `Content-Encoding: gzip` to
  avoid double compression, so only enable it if your app doesn't already
  compress. WebSocket upgrades are never compressed.

## Streaming, protocols & features

- **Responses are always streamed** (`FlushInterval = -1`) — SSE, chunked
  responses, gRPC-Web-style streams, and large downloads flow through without
  buffering. (The `exposure.streaming` flag is accepted and recorded but
  streaming is unconditional.)
- **WebSockets** pass through (the upgrade is forwarded; compression is
  skipped for upgrades).
- **HTTP/2**: the public side negotiates h2; the backend side can speak **h2c**
  when `exposure.h2c` is set (uses `http2.Transport` in cleartext). Otherwise
  HTTP/1.1 with `ForceAttemptHTTP2`.
- **Custom headers**: `exposure.headers` are applied to the upstream request
  *and* set on the response (`ModifyResponse`).
- **Timeouts** (per app `exposure.timeouts`): `read` → upstream response-header
  timeout; `write` → context deadline on the request; `idle` → upstream idle
  connection. Server-side: `ReadHeaderTimeout` 15s, `IdleTimeout` 120s.
- **Max body** (`exposure.maxBodyBytes`): oversized requests get `413` via
  `http.MaxBytesReader` (checked against `Content-Length` first).
- **Path routing** (`exposure.pathPrefix`): longest-prefix wins, so `/api` and
  `/` can point at different services on the same host.

## Security

- **Trusted proxies / header spoofing.** `X-Forwarded-For`, `X-Forwarded-Proto`,
  `X-Forwarded-Host`, and `Forwarded` are trusted **only** from addresses in
  `security.trustedProxies` (IP or CIDR). From an untrusted client the proxy
  **overwrites** `X-Forwarded-For`/`X-Real-IP` with the real peer and **deletes**
  `X-Forwarded-Host` and `Forwarded`, so a client cannot forge its origin.
  `X-Forwarded-For` is appended to (not replaced) only for trusted hops.
- **Route header injection** is explicit config (`exposure.headers`) — clients
  cannot add proxy headers by themselves.
- **No route → `404`**, upstream dial/response failure → `502`; both are generic
  and don't leak upstream details.
- **TLS 1.2+**, ACME host policy restricts issuance to permitted domains, and
  manual certs must have a matching key.
- **No credentials live in the proxy.** It talks to the control plane over a
  Unix control socket guarded by a control token; it never holds kubeconfig or
  registry secrets.
- **Snapshot validation** rejects malformed routing data before it can be served
  (defense against a bad config taking the edge down).

### What the proxy deliberately does *not* do

Authentication, authorization, rate limiting, WAF/bot filtering, request
caching, client-certificate (mTLS) auth, HTTP/3/QUIC, Brotli, sticky sessions,
or response rewriting. Put those in front of it (CDN/WAF/tunnel) or implement
them in your app; Menina stays a fast, transparent router.

## Endpoints

On the proxy listeners:

- `GET /_menina/health` and `GET /_menina/ready` → `{"status":"ok",
  "snapshotVersion":N,"routes":M}`.
- ACME HTTP-01 challenge when in `acme` mode.

Inspect routing without touching the proxy:

- `menina routes [--json]` — the host → service table from the control plane.
- `menina status` — overall health, including proxy/service state.
- `menina serve` exposes `menina_routes` (route count) on its `/metrics`
  endpoint.

## Configuration reference

Platform (`config.yml`):

```yaml
platform:
  httpListen: ":80"
  httpsListen: ":443"
tls:
  terminate: true
  mode: acme            # acme | manual | none
  httpRedirect: true
  certDir: /var/lib/menina/certs
  acme:
    email: ops@example.com
    # directoryUrl: https://acme-staging-v02.api.letsencrypt.org/directory
    # storageDir: /var/lib/menina/certs/acme
networking:
  mode: public          # public | cloudflare-tunnel
  baseDomains: [example.com]
  publicIp: 203.0.113.10
security:
  trustedProxies: [10.0.0.0/8, 192.168.0.0/16]
```

Per app (`apps/<app>.yml`):

```yaml
exposure:
  enabled: true
  hosts: [app.example.com]
  pathPrefix: /            # longest prefix wins
  redirectHttp: true       # per-route override of tls.httpRedirect
  headers: { X-Frame-Options: DENY }
  compression: false       # on-the-fly gzip
  h2c: false               # clear-text HTTP/2 to the backend
  maxBodyBytes: 0          # 0 = unlimited
  timeouts: { read: 30s, write: 60s, idle: 120s }
```

Environments can override any of these per app, so `staging.example.com` and
`app.example.com` can expose the same app differently.

## Failure modes

- **Control plane down**: the proxy keeps serving the last good snapshot and
  retries; routing does not flap. Metrics/status degrade, traffic doesn't.
- **App Service missing/`ClusterIP: None`**: that route is dropped (logged),
  others are unaffected.
- **Bad config**: `Swap` rejects the snapshot, the proxy keeps the previous
  table and logs `rejected routing snapshot`.
- **Upstream error**: `502` with a logged upstream cause.
- **Graceful shutdown**: on `SIGTERM` the listeners drain with a 20s timeout.

## Tests

The proxy package has focused tests for routing table validation/lookup
(longest prefix, duplicate rejection, atomic swap) and TLS/manual cert handling
(`internal/proxy/*_test.go`), plus the shared streaming/gzip/forwarded-header
behavior exercised through `httptest`.
