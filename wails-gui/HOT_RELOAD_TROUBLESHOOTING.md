# Wails Dev Hot Reload Troubleshooting

Use this guide to decide in under 30 seconds whether hot reload is blocked by a dirty local environment or by a watcher problem.

If you start the app with `./scripts/start-wails-gui.sh`, the launcher now reaps stale dev processes for the same real workspace path before calling `wails dev` and pins `VITE_PORT=5173`.

## Scope

This guide applies to `wails-gui/` only.

- Frontend hot reload is handled by the Vite dev server started by Wails.
- Go hot reload is handled by `wails dev` watching `.go` files.

Relevant config:

- [`wails.json`](wails.json)
- [`frontend/vite.config.ts`](frontend/vite.config.ts)
- [`main.go`](main.go)
- [`scripts/start-wails-gui.sh`](scripts/start-wails-gui.sh)

## 30-Second Preflight

Run these checks before starting `wails dev`:

```bash
pwd -P
pgrep -fl 'wails dev|vite|Yansu'
lsof -nP -iTCP:5173 -sTCP:LISTEN
lsof -nP -iTCP:8765 -sTCP:LISTEN
lsof -nP -iTCP:34115 -sTCP:LISTEN
test -d wails-gui/frontend/node_modules && echo node_modules:ok
test -d wails-gui/frontend/dist && echo dist:ok
```

Healthy state:

- `pwd -P` points to the real workspace path you expect.
- Only one related `wails dev` process exists for that real path.
- Only one related `vite` process exists for that real path.
- Only one `Yansu` app process exists for that real path.
- Port `5173` is either free before launch or clearly owned by the Vite process you just started.
- Port `8765` is not already occupied by an older `Yansu` instance.
- Port `34115` is not already occupied by an older `Yansu` instance.
- `frontend/node_modules` exists.
- `frontend/dist` exists.

Dirty state:

- Multiple `wails dev`, `vite`, or `Yansu` processes point at the same real path.
- `5173` is already occupied before a new launch.
- `8765` or `34115` is already occupied before a new launch.
- The visible workspace path is a symlink and you are mixing it with the real path when starting or debugging.
- `frontend/dist` is missing even though [`main.go`](main.go) embeds it.

## Decision Tree

Start here after you edit a file and the app does not update as expected.

### A. Frontend change did not appear

1. Check the `wails dev` terminal for a Vite HMR line such as:

```text
[vite] hmr update /src/App.tsx
```

2. If that line appears:

- The frontend watcher is working.
- The problem is not "watcher dead".
- Look for stale app instances, stale browser/webview state, or a mismatched Vite server port.

3. If that line does not appear:

- Check whether the active Vite process is the one tied to your current real path.
- Check whether Vite moved off `5173`.
- Check whether Wails detected the correct frontend dev server URL.
- Re-check for multiple Vite processes.

Likely dirty-environment signals:

- `Port 5173 is in use, trying another one...`
- More than one `vite` process
- The Vite process path points to a different real workspace than the one you think is active

### B. Go change did not appear

1. Confirm the process was started with plain `wails dev` and not `-nogorebuild`.

Expected here:

- [`scripts/start-wails-gui.sh`](scripts/start-wails-gui.sh) uses `wails dev -debounce ...`
- No `-nogorebuild`

2. Check the terminal after a real Go file save.

Expected when Go reload is healthy:

- Wails rebuild activity
- App restart activity
- No permanent port bind failures

3. If you see restart symptoms followed by bind failures:

- The Go watcher likely fired.
- The reload handoff failed because an older app instance still owns a port.

Typical signals:

- `failed to listen on 127.0.0.1:8765`
- repeated daemon replacement logs
- multiple `Yansu` processes

4. If you see no rebuild signal at all after a real Go save:

- The watcher may not have fired.
- Before blaming Wails, first rule out editor/path issues:
  - editing a different real path than the running process
  - saving through a different workspace alias
  - testing with `touch` only instead of a real save

## Known Environment Failure Modes

### 1. Multiple dev instances on the same real path

Symptoms:

- one Vite server on `5173`, another on `5174`
- one old `Yansu` still owns `8765`
- new instance starts but logs bind failures
- app feels like it only updates after manual restart

Interpretation:

- Watchers may still be firing.
- The reload pipeline is dirty, not necessarily disabled.

### 2. Vite port drift

Current config allows port drift:

- [`frontend/vite.config.ts`](frontend/vite.config.ts) uses `strictPort: false`
- [`wails.json`](wails.json) uses `"frontend:dev:serverUrl": "auto"`

Symptoms:

- Vite silently moves from `5173` to `5174`
- Wails attaches to the wrong frontend dev server or becomes harder to reason about

### 3. Missing `frontend/dist`

Current app embeds `frontend/dist`:

- [`main.go`](main.go)

Symptoms:

- startup or rebuild becomes brittle on a fresh checkout
- Go-side reload problems are harder to interpret

### 4. Mixed symlink path and real path

Symptoms:

- process paths look different even though they point to the same files
- you believe only one workspace is running, but the OS sees the same real directory

Interpretation:

- This is an environment hygiene issue, not a watcher implementation issue.

## What Good Looks Like

After starting `wails dev`, a clean session should look like this:

- exactly one `wails dev`
- exactly one `vite`
- exactly one `Yansu`
- no pre-existing listener on `8765`
- frontend edits produce Vite HMR lines
- Go edits produce rebuild/restart activity without persistent bind failures

## Validation Template

Use this template when you want to classify a failure quickly:

```text
Real path:
Running wails dev count:
Running vite count:
Running Yansu count:
5173 owner:
8765 owner:
34115 owner:
frontend/node_modules:
frontend/dist:
Frontend HMR line seen:
Go rebuild line seen:
Bind failure seen:
Conclusion:
```

Conclusion rules:

- If HMR lines appear, frontend watcher is alive.
- If Go reload logs appear but bind failures follow, the environment is dirty.
- If neither watcher signal appears and the environment is clean, investigate the watcher itself next.
