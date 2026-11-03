# Activity Pipeline Browser Diagnostics Technical Design

## Problem

The Developer Tools activity-processing page imported three native App methods:
history read, manual evaluation, and history deletion. Browser preview rejected
all three because none was in the localhost RPC allowlist. Opening the page
therefore emitted a visible load error before an operator could inspect any
pipeline run.

Allowlisting all three methods would make the page appear functional but would
give an ordinary browser tab two state-changing controls. Developer preview
needs inspection parity, not mutation parity.

## Contract

| Capability | Native Wails app | Browser preview |
| --- | --- | --- |
| Read recent processing runs | yes | yes |
| Refresh the read model | yes | yes |
| Expand run details and filters | yes | yes |
| Trigger immediate processing | yes | no |
| Clear processing history | yes | no |

`GetActivityPipelineRuns` is a bounded read of the retained diagnostics table.
It is explicitly browser-RPC allowlisted. `RunActivityPipelineNow` and
`ClearActivityPipelineRuns` remain absent from the allowlist.

The frontend detects the runtime using the established browser bridge helper.
In browser preview it omits the two native-only mutation buttons, so every
visible control is usable and cannot produce an expected authorization error.
The native application keeps the complete toolbar.

## Security Boundary

- Browser preview remains localhost-only and uses the existing explicit RPC
  allowlist.
- The change does not expose task replay, activity deletion, pipeline dispatch,
  permission, authority, or execution methods.
- Reading or refreshing activity-processing history does not alter observer,
  Task Episode, Shadow, Initiative, permission, or authority ledgers.

## Verification

1. Browser RPC accepts `GetActivityPipelineRuns`.
2. Browser RPC rejects `RunActivityPipelineNow` and
   `ClearActivityPipelineRuns`.
3. Browser preview loads real processing history without a toast or console
   error and shows only read/refresh/filter/detail controls.
4. The native app continues to show manual evaluation and clear-history
   controls.
5. Focused allowlist, complete backend/frontend, production build, and live
   browser-preview checks pass.
