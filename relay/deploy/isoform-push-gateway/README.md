# Isoform push gateway

This optional service lets a relay use APNs without holding the APNs signing
key. The gateway uses a persistent X25519 private key to authenticate hubs and
mint opaque, hub-bound push grants. It does not store hub pairing data or
grants, and it never logs tokens, grants, signatures, keys or ciphertext.

The example Compose deployment binds the service to loopback on port 8792.
Put an HTTPS reverse proxy in front of it if the relay is on another machine;
keep the gateway endpoint private and configure the relay with its HTTPS
origin. The `ISOFORM_PUSH_GATEWAY_BEHIND_TLS_PROXY` flag only permits the
gateway's container listener to use HTTP behind that proxy; it does not make
the relay-to-gateway connection safe over public HTTP.

From the repository root, set the APNs key ID, team ID, topic, environment,
and path to the `.p8` key before starting Compose. Generate the gateway key
once inside the image as UID/GID 10001; the generator creates it with mode
`0600`. The output directory must be writable by that user:

```sh
sudo install -d -o 10001 -g 10001 -m 0700 secrets
mkdir -p "$HOME/.cache"
touch "$HOME/.cache/isoform-push-gateway-placeholder"
export ISOFORM_PUSH_GATEWAY_KEY_FILE="$HOME/.cache/isoform-push-gateway-placeholder"
export ISOFORM_RELAY_APNS_KEY_FILE="$HOME/secrets/AuthKey_XXXXXXXXXX.p8"
export ISOFORM_RELAY_APNS_KEY_ID=XXXXXXXXXX
export ISOFORM_RELAY_APNS_TEAM_ID=YYYYYYYYYY
export ISOFORM_RELAY_APNS_TOPIC=com.example.isoform
export ISOFORM_RELAY_APNS_ENVIRONMENT=production

docker compose -f relay/deploy/isoform-push-gateway/compose.yaml run --build --rm \
  --entrypoint /usr/local/bin/isoform-push-gateway \
  -v "$PWD/secrets:/keys" isoform-push-gateway \
  -generate-key /keys/gateway.key
rm "$HOME/.cache/isoform-push-gateway-placeholder"
export ISOFORM_PUSH_GATEWAY_KEY_FILE="$PWD/secrets/gateway.key"
export ISOFORM_DESKTOP_PUSH_GATEWAY_PUBLIC_KEY="$(
  docker compose -f relay/deploy/isoform-push-gateway/compose.yaml run --rm \
    --entrypoint /usr/local/bin/isoform-push-gateway isoform-push-gateway \
    -print-public-key
)"
docker compose -f relay/deploy/isoform-push-gateway/compose.yaml up --build -d
```

For an existing gateway key, make it readable by the container user with
`sudo chown 10001:10001 secrets/gateway.key` and retain mode `0600`. The Compose
key mount remains read-only.

Configure the relay with `ISOFORM_RELAY_PUSH_GATEWAY_URL` and
`ISOFORM_RELAY_PUSH_GATEWAY_ENVIRONMENT` together (`development` or `production`),
plus its hub ID, public key and derived hub key. Stamp the gateway URL,
environment and public key into desktop builds. On key rotation, grants and
each hub's derived key become invalid; restart the gateway and have every phone
register again.
Do not set direct `ISOFORM_RELAY_APNS_*` credentials on the relay. The gateway
health endpoint is `GET /v1/health`; `GET /v1/key` publishes its public key,
`POST /v1/register` accepts `token`, `environment`, `hubId` and `hubPub`, then
sends a grant bound to all four values by background APNs push. The grant
notification includes `hubId` and `hubPub`. Authenticated `POST /v1/push`
enforces strict JSON, replay, request-size, IP, hub and token limits.

For direct execution, `ISOFORM_PUSH_GATEWAY_ADDR` defaults to `127.0.0.1:8792`.
Set `ISOFORM_PUSH_GATEWAY_TLS_CERT` and `ISOFORM_PUSH_GATEWAY_TLS_KEY` together
to serve TLS directly, or set `ISOFORM_PUSH_GATEWAY_BEHIND_TLS_PROXY=true`
only when a trusted reverse proxy terminates TLS.

Set `ISOFORM_PUSH_GATEWAY_TRUSTED_PROXIES` to the proxy's peer IP or CIDR so
the 10/minute registration and 120/minute push limits use the last address in
`X-Forwarded-For` only from that proxy. It uses the relay's trusted-proxy
parsing and loopback default; `none` ignores forwarded addresses. When unset,
it falls back to `ISOFORM_RELAY_TRUSTED_PROXIES`, including the legacy
`YANSU_RELAY_TRUSTED_PROXIES` name, before using the loopback default.

The Compose example is an operational starting point, not a substitute for
TLS, firewalling, key rotation, backups of the APNs key, or monitoring your
own deployment.
