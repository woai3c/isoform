# Agents reservation API

Run the complete local test suite from the repository root (Node 22.13 or newer):

```sh
node --test website/tests/*.mjs
```

The suite uses Node's experimental `node:sqlite` in-memory database and a small D1 adapter. It does not contact Cloudflare or an email provider. The SQLite experimental warning and the simulated delivery failure log lines are expected.

## Contract

The native apps call `https://isoform.ai/api/agents`; requests and responses are UTF-8 JSON, with no CORS requirement. Names and email addresses are trimmed and lowercased. Hub UUIDs are trimmed and lowercased; language is normalized to `en` or `zh-CN`. Tokens are case-sensitive and are trimmed, never lowercased. A token is a canonical, unpadded base64url encoding of 32 random bytes. Only SHA-256 of the token string is stored. Each hub has at most one name while its reservation exists; releasing it removes its stored hash.

Names must match `^[a-z0-9](?:[a-z0-9]|\.(?=[a-z0-9])){2,29}$`. The reserved names are `isoform agents agent admin administrator support help www api app apps mail email root system status blog docs about login signup settings bo team staff official security billing`. Email must match `^[^\s@]+@[^\s@]+\.[^\s@]+$` and be at most 254 characters. This is a form check, not proof of ownership.

- `POST /check` accepts `name`, `candidates`, and optionally `hub` and `token`. It returns `{name, available}`; if unavailable it also returns `reason` (`taken`, `not_allowed`, or `invalid`) and `suggestion`. A valid matching hub/token makes its own name available. Suggestions are the first valid, unreserved and free candidate in order, followed by the requested name suffixed with 2 through 99 (falling back to numbering the first candidate when the requested name is empty or invalid). `suggestion` is null if none is free.
- `POST /reserve` accepts `hub`, `token`, `name`, `email`, `lang`, `candidates` and optionally `client` (`mac` or `iphone`, named in Bo's notices). A new request, or a change to a new email, returns `{name, email, reservedAt, status: "pending", expiresAt}`, emails a 6-digit code in `lang` and holds the name for 10 minutes. A request without a recognized `client` is stored as unconfirmed without email or a name hold, unless the hub is already verified, in which case a different email leaves its verified reservation unchanged. A pending request does not count in the line and sends Bo nothing. Changing a verified name with the same email needs no code (the hub token is the device's key), stays verified, keeps its line timestamps and returns `status: "verified"` with `place`. An email change is a separate pending hold while the original stays verified; the right code applies it and keeps the original's place. Repeating the same pending request sends a new code. Taken is 409 `{error: "taken", suggestion}`; invalid names, reserved names, and invalid emails are 422 with `invalid_name`, `not_allowed`, or `invalid_email`. A wrong token for an existing hub is 403 `{error: "token"}`.
- `GET /reservation?hub=<uuid>` with `Authorization: Bearer <token>` returns the reservation: `{name, email, reservedAt, status}` plus `expiresAt` while a hold or code is out, `place` when verified, and `oldName` when `status` is `choice`. While an email change is held it returns the change; after the hold ends, the verified original. 404 `{error: "none"}` if no reservation exists, 403 `{error: "token"}` if the token mismatches. `status` is `pending`, `verified`, `unconfirmed` (an app request stored before codes) or `choice` (the code was right but the email already has a page). The original `name`, `email`, and `reservedAt` fields remain for shipped clients.
- `POST /verify` accepts `hub`, `token` and `code`. The right code returns the verified reservation with `place` (Bo gets `Request:` or `Changed:` naming the Mac app or the iPhone app), or `status: "choice"` with `oldName` when the email already has a verified page; the app never learns that before the code. 400 `wrong_code`; 410 `expired` when the hold ran out or no code is out; 409 `taken` with `suggestion` when an unconfirmed request's name was confirmed by someone else (it is let go); 429 `rate`.
- `POST /resend` accepts `hub` and `token`. A new code replaces the old one and the hold restarts at 10 minutes. For an `unconfirmed` reservation this is Send code: the reservation stays unconfirmed with `expiresAt` while the code is out. 404 `none` when nothing is pending (the app then sends `/reserve` again), 409 `taken` as for verify, 429 `rate`.
- `POST /choose` accepts `hub`, `token` and `keep`. After `choice`: `keep: true` makes the email's existing page this device's page (with its place and website sessions) and lets this request go; `keep: false` moves the existing page's place to the requested name and lets the old name go (Bo gets `Changed:`). Returns the verified reservation, or 410 `expired`.
- `POST /cancel` accepts `hub` and `token` and lets a held request go (Change the name or email). A verified original stays. Returns `{reservation}` with what remains, or `null`.
- `POST /release` accepts `hub` and `token` and returns 200 `{}`; a mismatched token is 403. Releasing an absent reservation is idempotent. Bo is told only when a verified page is released.

Requests made before codes (migration `0006_agents_app_codes.sql`) are `unconfirmed`: they leave the line and the unique name slot (stored as `~u-{hub}` with the asked-for name in `unconfirmed_name`), so they show as free, and they are let go when anyone confirms that name. Confirming one with Send code keeps its original request time; the first to confirm a name gets it, ending another device's hold on it. Once after deployment, the first API request emails Bo the list of unconfirmed requests (name, email, time, source).

Reserve, resend, verify, choose, cancel and release count toward 20 writes per client IP per UTC hour. Code sends and wrong codes share the website's limit of five an hour per email and, for the apps, per device (hub). A 429 response includes `retryAt` and `Retry-After`. POST bodies above 4,096 bytes return 413. Expired holds are ignored by availability checks and deleted lazily on reads or a new claim. Only verified rows count toward places and referral credit. The database enforces unique names and hubs; confirmation atomically checks that the email has no other verified name, including legacy rows without an email hash.

The server receives the raw app token over TLS and stores only its SHA-256 hash. It also receives the candidate names supplied by the app. Codes have six digits, are stored salted and hashed, are used once and end when a new one is sent. The code email is plain text with no links, from "Bo at Isoform", in the app's language. A failed code delivery returns 503 `send_failed` and keeps the previous code and hold. Bo's notices are best effort. The `resend` provider is required for actual delivery to the person; `log` is for local testing, while `none` and `cloudflare` return a delivery error for codes.

## Deployment instructions for Bo, after approval

No resources or secrets were created by this implementation. The existing Git-integrated Pages site has an empty build command and output directory `website`. Confirm its current settings in the dashboard before changing anything. Pages discovers `/functions` at the **project root**, not its static output directory. Therefore, if the existing project root is the repository root (as the documented `website` output suggests), change **Root directory** to `website` and **Build output directory** to `.` together, keeping the build command empty; if the root is already `website`, retain it and use `.`. Confirm the build watch paths still include the site. This retains the same static files while making `website/functions` discoverable. See [Pages Functions setup](https://developers.cloudflare.com/pages/functions/get-started/) and [monorepo configuration](https://developers.cloudflare.com/pages/configuration/monorepos/). Review the project settings and test a preview before production.

`website/wrangler.toml` is deliberately local-only and omits `pages_build_output_dir`, so it does not become the Pages production configuration. Its `local-only` database ID is a placeholder. Do not deploy it as production configuration. Configure the existing project in the dashboard; if instead opting into Wrangler-managed configuration, first download and review the existing configuration as explained in [Cloudflare's configuration guide](https://developers.cloudflare.com/pages/functions/wrangler-configuration/), preserving all existing settings.

After approving resource creation and deployment:

1. From `website`, use a locally installed or `npx` Wrangler to create the production database:

   ```sh
   npx wrangler d1 create isoform-agent-reservations
   ```

   Note the returned database ID. Use a separate preview database for previews; never bind preview to production data.
2. Make an untracked temporary copy of `wrangler.toml` in `website`, replace `database_id = "local-only"` with the returned production ID, and apply the migration:

   ```sh
   cp wrangler.toml wrangler.deploy.toml
   # Edit wrangler.deploy.toml and insert the returned database ID.
   npx wrangler d1 migrations apply isoform-agent-reservations --remote --config wrangler.deploy.toml
   rm wrangler.deploy.toml
   ```

   Repeat with the separate preview database and its ID if using a preview. See [D1 migration commands](https://developers.cloudflare.com/d1/wrangler-commands/).
3. In the existing Pages project dashboard, add the D1 binding `AGENTS_DB` for the appropriate environment and select its database. Set `EMAIL_PROVIDER` to the approved provider in that environment. If using Resend, verify `isoform.ai` and the sender with Resend, then set its secret interactively without printing it:

   ```sh
   npx wrangler pages secret put RESEND_API_KEY --project-name <PAGES_PROJECT_NAME> --env production
   ```

   Configure a separate preview secret if preview testing requires it. If using Cloudflare instead, onboard the sender domain for Email Sending, verify `bo@isoform.ai` as a destination, and add a `send_email` binding named `AGENTS_EMAIL` restricted to that destination. Confirm this binding is available to the Pages project. This mode does not send confirmations to the person. See [send bindings](https://developers.cloudflare.com/email-service/configuration/send-bindings/).
4. Check that Email Routing and the existing MX/DNS configuration deliver mail addressed to `bo@isoform.ai`, including replies to confirmations. Review any provider-required SPF/DKIM/DMARC changes before making them, and send and reply to a test message after setup. Do not replace existing mail routes inadvertently.
5. After the settings and bindings are ready, deploy the approved commit through the existing Git integration. The worker branch commit carries `[CF-Pages-Skip]` to prevent an automatic preview deployment; create or select an approved deployment without that skip flag when ready. Verify the static home page, every API endpoint, both languages and actual email delivery in a preview first, then promote through the normal production branch workflow. Check the deployment's functions routes and logs; a static home page alone does not prove the API was deployed.
