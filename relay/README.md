# Isoform relay

The relay is a store-and-forward broker between an iPhone and the user's Mac
(the hub). Phones reach it over HTTPS with long-polls. The Mac keeps one outbound
WebSocket open to it. The relay stores credential digests, ciphertext envelopes,
encrypted briefs, APNs tokens and routing metadata. It never holds a private key,
a plaintext brief or a plaintext request, and it never decrypts anything.

The relay is not a sync service. It does not read the hub's data, run models, or
decide what a phone may do. Scope for a request's path is enforced by the hub,
because the path sits inside the ciphertext.

The wire contract is `mobile-protocol/PROTOCOL.md`. Sections 5 (device HTTP API)
and 6 (hub WebSocket) describe this service. Section 9 describes the push payload,
and section 14 the sealed moment notices and receipts. Sections 18 and 19 describe
text messages and email, the two places the relay handles plaintext: it sends the
texts and emails a hub writes through an SMS or mail provider (texts' replies come
back), keeping numbers and addresses only hashed and sealed under its own keys
(`docs/sms-channel.md` and `docs/email-channel.md` cover switching a provider on). A
client address may create at most 20 new hub ids an hour.

## Layout

- `cmd/isoform-relay`: process entry point, listener and shutdown.
- `internal/config`: `ISOFORM_RELAY_*` settings. Non-loopback listeners require TLS.
- `internal/httpapi`: device routes, hub WebSocket upgrade, `{ok,data,error}` replies.
- `internal/hub`: one live connection per hub, register first, `replaced` takeover,
  frame validation, replay of queued requests and claims on connect.
- `internal/store`: the only SQLite owner (`relay.sqlite`, WAL, foreign keys).
- `internal/push`: APNs registrations, the lease/retry outbox, and the notice
  header checks (`notice.go`).
- `internal/apnspush`: HTTP/2 provider client, payload per push kind, and outbox worker.
- `internal/httpapi/notices.go`: `POST /v1/notices/{noticeId}/receipts`.
- `internal/wire`: envelope shape, strict JSON decoding and byte-level validators.
- `internal/notify`, `internal/limit`: in-process wake-ups and fixed-window limits.
- `internal/sms`: text messages: the provider seam (Twilio and a test fake), number
  verification, STOP/START/HELP, caps, and the reply queue; `internal/httpapi/sms.go`
  serves the hub routes and the provider webhook.
- `internal/email`: email for playbook results: the provider seam (SMTP and a test fake),
  address verification and caps; `internal/httpapi/email.go` serves the hub routes. A send
  goes only to the hub's verified address (`docs/email-channel.md`).

## Configuration

Legacy `YANSU_RELAY_*` variables remain accepted as fallbacks. When an old
variable is used, the relay logs a value-free deprecation message. If both
spellings are set to different values, startup fails and names both variables.

| Variable | Default | Meaning |
| --- | --- | --- |
| `ISOFORM_RELAY_ADDR` | `127.0.0.1:8791` | Literal IP and port to listen on. |
| `ISOFORM_RELAY_STATE_DIR` | `./data` | Directory for `relay.sqlite` (created mode 0700). |
| `ISOFORM_RELAY_TLS_CERT`, `ISOFORM_RELAY_TLS_KEY` | unset | PEM certificate and key. Set together. Required on any non-loopback address. |
| `ISOFORM_RELAY_TRUSTED_PROXIES` | `127.0.0.0/8,::1/128` | IP addresses or CIDR ranges of the proxy in front of the relay, whose client header names the client for per-address limits. |
| `ISOFORM_RELAY_BEHIND_TLS_PROXY` | `0` | `1` allows a plaintext listener off loopback because a proxy named in `ISOFORM_RELAY_TRUSTED_PROXIES` (which must then be set) terminates TLS, as on Fly. Exclusive with the TLS files. |
| `ISOFORM_RELAY_CLIENT_IP_HEADER` | `X-Forwarded-For` | The header a trusted proxy puts the client address in; its last value counts. Fly sets `Fly-Client-IP`. |
| `ISOFORM_RELAY_MAX_CONNS` | `4096` | Open connections the listener accepts at once; a hub socket or a long-poll holds one. |
| `ISOFORM_RELAY_LOG_FORMAT` | `text` | `json` writes one object per line (time, level, msg) for a log collector. |
| `ISOFORM_RELAY_APNS_KEY_FILE` | unset | Absolute path to the P-256 PKCS8 `.p8` key (owner-only). |
| `ISOFORM_RELAY_APNS_KEY_ID`, `ISOFORM_RELAY_APNS_TEAM_ID` | unset | 10-character Apple identifiers. |
| `ISOFORM_RELAY_APNS_TOPIC` | unset | Bundle identifier used as the APNs topic. |
| `ISOFORM_RELAY_APNS_ENVIRONMENT` | unset | `development` or `production`. One environment per process. |
| `ISOFORM_RELAY_SMS_PROVIDER` | unset | `twilio` turns text messages on. Unset, the text routes answer 503. |
| `ISOFORM_RELAY_SMS_KEY` | unset | 32 random bytes in base64 (`openssl rand -base64 32`). Seals numbers and replies at rest (and keys the lookup hashes while `ISOFORM_RELAY_SMS_HASH_KEY` is unset); losing it orphans every subscription. Required with a provider. |
| `ISOFORM_RELAY_SMS_HASH_KEY` | `ISOFORM_RELAY_SMS_KEY` | 32 bytes in base64. Keys the number hashes that opt-outs, usage and lookups are stored by. Never rotate it: changing it forgets every STOP. Set it to the first `ISOFORM_RELAY_SMS_KEY` before rotating that key. |
| `ISOFORM_RELAY_SMS_KEY_PREVIOUS` | unset | The sealing key being rotated out. With it set, the relay opens values sealed under it and reseals them under `ISOFORM_RELAY_SMS_KEY` at start; remove it after that start (`docs/sms-channel.md`). |
| `ISOFORM_RELAY_SMS_HELP_CONTACT` | unset | Support email or web address quoted in the HELP answer. Required with a provider. |
| `ISOFORM_RELAY_SMS_WEBHOOK_URL` | unset | The exact public URL configured at the provider for incoming messages, for example `https://relay.example.com/v1/sms/inbound`. |
| `ISOFORM_RELAY_TWILIO_ACCOUNT_SID`, `ISOFORM_RELAY_TWILIO_AUTH_TOKEN`, `ISOFORM_RELAY_TWILIO_MESSAGING_SERVICE_SID` | unset | Twilio account, its auth token (also the webhook signing key) and the Messaging Service that holds the sending number. |
| `ISOFORM_RELAY_SMS_DAILY_CAP`, `ISOFORM_RELAY_SMS_MONTHLY_CAP` | `10`, `200` | Texts per number per UTC day and month. |
| `ISOFORM_RELAY_SMS_HOURLY_CAP`, `ISOFORM_RELAY_SMS_CODES_PER_HOUR` | `500`, `30` | Relay-wide texts and verification codes an hour. |
| `ISOFORM_RELAY_SMS_KEYWORD_ANSWERS_PER_HOUR` | `20` | Relay-wide HELP and START answers an hour, kept apart from the hourly text cap. |
| `ISOFORM_RELAY_EMAIL_PROVIDER` | unset | `smtp` turns email on. Unset, the email routes answer 503. |
| `ISOFORM_RELAY_EMAIL_KEY` | unset | 32 random bytes in base64. Seals addresses at rest and keys their lookup hashes. Required with a provider. |
| `ISOFORM_RELAY_EMAIL_FROM` | unset | The sender, an address or `Name <address>`, on a domain the mail provider is set up to send for. |
| `ISOFORM_RELAY_SMTP_HOST`, `ISOFORM_RELAY_SMTP_PORT` | unset, `587` | The provider's SMTP submission server. |
| `ISOFORM_RELAY_SMTP_USERNAME`, `ISOFORM_RELAY_SMTP_PASSWORD` | unset | SMTP credentials (set both or neither). |
| `ISOFORM_RELAY_SMTP_MODE` | `starttls` | `starttls` (port 587) or `tls` (port 465); `plain` only for a server on the relay's own machine. |
| `ISOFORM_RELAY_EMAIL_DAILY_CAP`, `ISOFORM_RELAY_EMAIL_HOURLY_CAP`, `ISOFORM_RELAY_EMAIL_CODES_PER_HOUR`, `ISOFORM_RELAY_EMAIL_CODES_PER_DAY` | `20`, `300`, `30`, `200` | Emails per hub per UTC day, relay-wide emails an hour, relay-wide verification codes an hour and a day. |
| `ISOFORM_RELAY_SMS_COUNTRIES` | `+1` | Calling codes numbers may have, comma separated. `+1` admits US and Canadian personal numbers only. Any other code admits every number under it, premium-rate and satellite ranges included, with no filter on the relay: add one only with the provider's geographic permissions limited to that country. |

The five APNs settings are all-or-nothing. Without them, push registration
returns 503 and the relay sends no notifications. Other environments get 503.
Text messages need the provider, the key, the help contact, the webhook URL and
the provider's own settings together; with the provider set, the relay refuses to
start while any of them is missing or malformed.

## Run locally

```sh
cd relay
go build -o isoform-relay ./cmd/isoform-relay
ISOFORM_RELAY_STATE_DIR="$(mktemp -d)" ./isoform-relay
curl -s http://127.0.0.1:8791/v1/health
```

Tests use real HTTP, real WebSockets and a real SQLite file in a temporary
directory. They use no mocks for the store.

```sh
go test ./... -timeout 180s
```

## Docker

`compose.yaml` builds from this directory alone and runs as UID 10001 with a
read-only root filesystem, all capabilities dropped, and TLS files mounted from
the host. Set `ISOFORM_RELAY_ENV_FILE`, `ISOFORM_RELAY_TLS_CERT_FILE` and
`ISOFORM_RELAY_TLS_KEY_FILE` to absolute paths outside the checkout. The file
header documents the TLS-proxy alternative and the APNs key mount.

## Operational notes

- Back up `relay.sqlite` with `sqlite3 relay.sqlite ".backup relay-backup.sqlite"`
  while the relay runs. Copying the file alone can miss WAL contents.
- Restoring an older backup rolls the relay back to that point. Devices and
  pairings created after the backup are gone, and their phones must pair again.
- Abuse limits are fixed windows held in memory and reset on restart: claims
  10 per 10 minutes per pairing and per client address; device requests 60 per
  minute per device. A forwarded client address (`X-Forwarded-For`) is believed
  only from a peer in `ISOFORM_RELAY_TRUSTED_PROXIES` (loopback by default: a
  proxy on the same host). Any other peer, a private one included, counts as
  the client itself, so a client cannot pick the address its limits are
  counted under. The trade-off: the relay serving TLS itself behind Docker's
  default port publishing (as `compose.yaml` does) sees every client arrive
  from the bridge gateway, which is not trusted, so every per-address limit
  (10 claims per 10 minutes, 10 verification texts and emails an hour, 20 new
  hub ids an hour, 60 bad webhook posts a minute, all counted per IPv4 address
  or IPv6 /64) becomes one bucket for everyone, and one client can use it up
  for all. Run a proxy (Caddy, nginx) on the same host in front of a
  loopback-bound relay, as recommended above, so the default trust applies;
  or list the proxy's address in `ISOFORM_RELAY_TRUSTED_PROXIES` and have it
  overwrite `X-Forwarded-For`. The per-pairing, per-hub, per-number and
  relay-wide limits do not depend on the address.
- Queues are bounded: 200 requests or responses per device and direction, and
  50 hub `event` envelopes per device. A full request queue returns 429 and
  nothing is dropped. A full response queue drops its oldest responses, and the
  event queue drops its oldest events; each kind is trimmed on its own, so a
  burst of events never evicts a response the phone has not read. Requests
  expire after one hour. Each device keeps its newest seven briefs.
- Each push carries its kind: `brief` for a stored brief, `moment` for a sealed
  notice, and `needs_you` for an `out` event sent with `notify`. `needs_you` is
  the retired release 2 Desk push: current Macs never send it, and the relay
  keeps accepting it only for Macs on an older build (PROTOCOL.md section 9). The kind
  selects the fixed alert text. A `moment` takes its APNs level, category and
  collapse id from the hub, and its alert text is the fallback for its moment
  kind (section 14.4). The relay never opens a notice; it stores the sealed text
  and refuses one over 3,500 characters or a payload over 4 KiB.
- Receipts (`delivered`, `opened`, `acted`, `dismissed`) are stored once per
  notice, device, state and action, and kept 30 days. A new receipt goes to the
  hub as a `receipt` frame; receipts stored while the hub was away go out when it
  connects. A receipt queued on a socket that drops before the write is not
  replayed.
- Logs carry only short identifiers and counts. They never include bodies,
  tokens, device names, addresses or ciphertext.
- The hub credential is trust on first use: the first connection that presents a
  hub id fixes its credential digest.

## Push gateway

The optional Isoform push gateway lets a self-hosted relay send APNs requests without placing APNs
credentials in the desktop app. Build and run `cmd/isoform-push-gateway` separately from the relay.
The relay must be configured with both `ISOFORM_RELAY_PUSH_GATEWAY_URL` and
`ISOFORM_RELAY_PUSH_GATEWAY_ENVIRONMENT` (`development` or `production`); the URL must use HTTPS
except for loopback development. These settings are mutually exclusive with direct
`ISOFORM_RELAY_APNS_*` configuration.

The gateway requires `ISOFORM_PUSH_GATEWAY_KEY_FILE`, a base64url X25519 private-key file with mode
`0600`. Use `go run ./cmd/isoform-push-gateway -generate-key <path>` once and publish the matching
public key from `-print-public-key` as `ISOFORM_DESKTOP_PUSH_GATEWAY_PUBLIC_KEY` for the desktop
build. `GET /v1/key` exposes that public key; `POST /v1/register` delivers hub-bound grants through
background APNs pushes; `POST /v1/push` requires a signed, replay-protected request and a valid
grant. The gateway is stateless about hubs and grants and does not log secrets. Rotating its key
invalidates grants and hub MAC keys, so phones must register again.

APNs credentials are configured on the gateway with `ISOFORM_RELAY_APNS_*`; legacy
`YANSU_RELAY_APNS_*` names are also accepted as deprecated fallbacks. The supplied Compose
deployment mounts both keys read-only and drops capabilities; provide TLS or place it behind a
trusted HTTPS reverse proxy before exposing it beyond loopback.

The gateway validates the configured push environment, applies per-IP and per-token limits, and
does not persist requests. It never logs push tokens or notice ciphertext. Its APNs payload format
and encrypted notice content are the same as the direct relay push path.
