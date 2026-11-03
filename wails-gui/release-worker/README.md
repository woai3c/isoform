# release-worker

A Cloudflare Worker that resolves `/latest/<filename>` requests on `release.yansu.app` to the current versioned download URL, keeping public-facing download links stable while the underlying artifacts carry version numbers.

## Purpose

When a new desktop release is published, artifact filenames include the version (e.g. `yansu-darwin-arm64-v0.2.0.dmg`). This worker lets the website and docs always link to stable `/latest/` URLs — the worker looks up the current version from `version.json` and issues a `302` redirect to the real file.

All non-`/latest/` paths (versioned paths, `version.json`, etc.) bypass the worker entirely and hit the R2 bucket's custom domain directly.

## How It Works

For each request to `/latest/<filename>`:

1. Parse the platform key from the filename (e.g. `darwin-arm64`, `windows-x64`).
2. Fetch `version.json` from `release.yansu.app`, using the Cloudflare Cache API (60-second TTL) to avoid hammering the manifest on every download click.
3. Look up `manifest.downloads[platform]` for the user-facing artifact URL (`.dmg`, `.zip`, `.tar.gz`). The updater fields are intentionally skipped: `updater_assets` points to current self-update archives, while `assets` is reserved for legacy updater payloads.
4. Redirect the client with a `302` to the resolved URL.

If anything goes wrong (missing manifest, unknown platform, loop detection), the request is passed through to the origin unchanged so the worker never takes the download path down.

## File Structure

```
release-worker/
├── src/
│   └── index.ts       # Worker entry point — fetch handler + helpers
├── wrangler.toml      # Cloudflare Worker config and route binding
├── package.json       # npm scripts and dev dependencies
└── tsconfig.json      # TypeScript compiler config (ES2022, strict)
```

## Key Components

### `src/index.ts`

| Symbol | Purpose |
|---|---|
| `fetch` handler | Entry point; dispatches each request through platform parse → manifest fetch → redirect |
| `parsePlatform(filename)` | Extracts `darwin-arm64`, `windows-x64`, or `linux-arm64` from the filename |
| `fetchManifest(ctx)` | Fetches and caches `version.json`; stores result in `caches.default` for 60 s |
| `wouldLoop(target, requestUrl)` | Guards against a misconfigured manifest that points back at `/latest/*`, which would cause infinite redirects |

### `version.json` manifest shape (served from R2)

```json
{
  "version": "0.2.0",
  "downloads": {
    "darwin-arm64": "https://release.yansu.app/v0.2.0/yansu-darwin-arm64.dmg",
    "darwin-x64":   "https://release.yansu.app/v0.2.0/yansu-darwin-x64.dmg",
    "windows-x64":  "https://release.yansu.app/v0.2.0/yansu-windows-x64.zip",
    "linux-x64":    "https://release.yansu.app/v0.2.0/yansu-linux-x64.tar.gz"
  },
  "updater_assets": {
    "darwin-arm64": "https://release.yansu.app/v0.2.0/yansu-darwin-arm64.app.tar.gz",
    "darwin-x64":   "https://release.yansu.app/v0.2.0/yansu-darwin-x64.app.tar.gz",
    "windows-x64":  "https://release.yansu.app/v0.2.0/yansu-windows-x64.zip",
    "linux-x64":    "https://release.yansu.app/v0.2.0/yansu-linux-x64.tar.gz"
  }
}
```

Only the `downloads` field is consulted by this worker. The desktop app's updater uses `updater_assets` when present and falls back to `assets` only for manifests generated before `updater_assets` existed. Keep `assets` available for older clients that still expect single-file `.gz` update payloads.

## Dependencies

| Package | Role |
|---|---|
| `wrangler` | Cloudflare Workers CLI — local dev, deploy, tail logs |
| `@cloudflare/workers-types` | TypeScript types for the Workers runtime (`ExecutionContext`, `caches`, etc.) |
| `typescript` | Compiler (compile-check only; `noEmit: true`) |

## Route Binding

Defined in `wrangler.toml`:

```toml
[[routes]]
pattern = "release.yansu.app/latest/*"
zone_name = "yansu.app"
```

Only `release.yansu.app/latest/*` is handled by this worker. All other paths on that domain serve directly from R2.

## Usage

### Local development

```bash
npm install
npm run dev      # starts wrangler dev server on localhost
```

### Deploy

```bash
npm run deploy   # wrangler deploy — pushes to Cloudflare
```

### Tail live logs

```bash
npm run tail     # streams logs from the deployed worker
```

## Configuration

No environment variables are required. The manifest URL is hardcoded:

```ts
const MANIFEST_URL = "https://release.yansu.app/version.json";
const MANIFEST_CACHE_TTL_SECONDS = 60;
```

To point the worker at a different origin or change the cache lifetime, edit those constants in `src/index.ts`.

## Deployment Context

This worker is part of the Yansu Desktop (wails-gui) release pipeline. The broader release flow is:

1. `build-wails.sh` compiles platform artifacts and generates `version.json`.
2. Artifacts and `version.json` are uploaded to an R2 bucket behind `release.yansu.app`.
3. This worker intercepts `/latest/*` requests and redirects them to the versioned path recorded in `version.json`.

See `wails-gui/scripts/` for build and upload scripts.
