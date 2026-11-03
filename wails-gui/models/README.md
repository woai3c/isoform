# Model catalog and confirmed downloads

`manifest.json` and `evaluation.md` are embedded in the Go application.
`Manager` owns planning, confirmation, durable jobs, verified transfers and
atomic installation. A feature loader calls `Require`; that call never fetches.
The frontend contract and integration limits are recorded in
[`docs/hub-reviews/claude-codex-setup-backend-self.md`](../../docs/hub-reviews/claude-codex-setup-backend-self.md).

Refresh metadata from the repository root:

```bash
python3 wails-gui/models/refresh_manifest.py
```

This is a maintainer operation that streams several gigabytes from the current
upstream download URLs to calculate sizes, SHA-256 values and archive-member
metadata. It stores metadata only, not model payloads. Large MLX weight files
use their upstream Hugging Face LFS size and SHA-256 metadata; small files are
fetched and hashed. Unreachable/unresolved metadata is explicitly unknown and
cannot authorize a download. Review source URLs, licences, languages, default
picks and runtime support before committing a refreshed catalog.

The script also extracts the relevant raw WER/RTF table rows from
`asr-eval/RESULTS.md` and copies the whole evaluation document for the app.
The schema/evidence and downloader tests run with `go test ./models` from
`wails-gui/`; HTTP tests use small local fixture files and a real local server.
They do not fetch production models or read an installed application's data.

Readiness requires pinned file hashes; size alone is never sufficient. On
macOS/Linux, successful checks are cached only while file identity, size,
modification time and kernel change time remain unchanged. Legacy Qwen installs
can retain the verified archive marker plus pinned required runtime files;
README and sample WAVs are not required for those installs.

Every launch shares remaining transfer and extraction reservations with active
jobs. Native macOS launches also wait for a known, online, non-expensive, unconstrained
Network.framework path. Paused confirmed jobs retain their source and partials;
network changes and `ResumeModelDownload` recheck both path and disk admission.

`GetModelNetworkState` exposes the same native path snapshot before download
confirmation. Network changes use `models:downloads` to refresh both the sheet
and download status, including when there are no jobs. Reading this snapshot
does not authorize any new model downloads.
