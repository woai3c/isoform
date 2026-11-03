# Computer Use App Access Technical Design

Status: implementation contract for `vision/shadow-agent`

## Decision

Yansu accesses first-party work applications through Computer Use and the
accounts already signed in on the user's Mac. It does not ask the user to
connect each provider through OAuth, and it does not fall back to provider APIs
or managed connector MCP servers.

The product mode is frozen as:

```text
mode = computer_use_only
oauth_fallback = false
policy = computer-use-app-access-v1
```

The initial app catalog is Gmail, Google Calendar, Google Drive, Google Docs,
Slack, Notion, GitHub, Linear, and Microsoft Teams. Supported browser routing
includes Safari, Chrome, Arc, Edge, and Brave.

## Boundaries

This decision replaces first-party provider connection UX and managed Composio
runtime access. It does not replace:

- Yansu's own account authentication;
- optional inbound messaging integrations used to contact Yansu;
- developer-installed generic MCP servers, whose remote server may require its
  own OAuth protocol;
- explicit source consent;
- action authority.

Computer Use permission answers only whether Yansu can operate local UI. It
does not answer whether Yansu may inspect a private source or perform an
external side effect.

```text
Computer Use permission
  -> local UI transport is available

source grant
  -> one exact read surface may be inspected for one declared purpose

authority grant
  -> one exact class of production effects may be executed
```

No gate implies either of the others.

## Runtime Architecture

`GetComputerUseAppAccessSnapshot` derives a read-only readiness projection from
the live Computer Use driver, macOS Accessibility and Screen Recording status,
screen-lock state, the fixed app catalog, and fixed browser catalog.

App-specific behavior is implemented as a bounded action compiler, not a broad
agent prompt. A compiler must define:

- target app and allowed browser bundles;
- exact starting URL or navigation rule;
- exact Computer Use operations and parameters;
- source-consent requirement;
- visibility and screenshot policy;
- extraction limits;
- cleanup action;
- terminal receipts;
- cancellation and lock behavior.

Before each compiler step reaches the Computer Use daemon, it passes through
the shared authority evaluator. The exact active source grant materializes only
two run-scoped capabilities: read perception and reversible local transport
setup/cleanup. The resulting evaluation and grant ID are written to the
authority ledger. Unknown Computer Use operations fail before the daemon call.
No external-write capability is materialized from a source grant.

The first compiler is `gmail.computer_use.read` for Background Understanding.
Its production sequence is:

```text
verify exact Gmail source grant
  -> verify Computer Use and unlocked Mac
  -> list_apps
  -> choose a supported browser, preferring an existing Gmail window
  -> launch_app(activates=false)
  -> open_url(frozen bounded Gmail search for the granted mailbox)
  -> list_windows
  -> choose only a Gmail or Google sign-in window
  -> get_window_state(include_screenshot=false, depth=40)
  -> verify the target window and exact granted query are visible
  -> extract bounded visible row metadata/snippets
  -> press_key(cmd+w) only after target confirmation
  -> persist hashes, action receipts, and bounded summaries
```

The reader fails closed if the Mac locks, permissions are missing, the source
grant is absent or stale, no supported browser is available, Gmail is signed
out, the target window is ambiguous, the exact granted query is not visible, or
the accessibility snapshot cannot be parsed. It never closes an unconfirmed
tab and never changes labels, drafts, sends, downloads, or opens an attachment.

## Session And Privacy Model

The browser owns provider credentials. Yansu does not receive, persist, refresh,
or exchange provider access tokens. Users sign in through the provider's normal
browser UI when necessary.

Background Gmail does not persist screenshots, raw accessibility trees, raw
window titles, message bodies, attachments, or full Computer Use results. It
persists the selected browser identity, target-window identity, snapshot
ID/hash, action names, cleanup status, opaque source hashes, bounded counts, and
attributed summaries.

Interactive production actions may use richer Computer Use perception only
under their own capability and receipt contracts. This app-access policy does
not expand those contracts.

## Product Experience

Settings > App access is the single first-party readiness surface. It shows:

- live Computer Use readiness and policy version;
- whether the Mac is locked;
- the supported application catalog;
- a direct link to open each application's ordinary sign-in page;
- macOS permission review.

Onboarding teaches one outcome: enable Computer Use once, then work in the apps
already used every day. It has no provider connect carousel and no OAuth
fallback. Legacy provider deep links resolve to App access.

The app catalog and composer capability chips reflect shared Computer Use route
readiness. They do not claim that a provider account is authenticated merely
because a browser is available. Sign-in is verified only when a bounded
app-specific operation reaches the target surface.

## Legacy Managed Connector Containment

Managed Composio access is disabled at every reachable boundary:

- status returns no first-party managed connections;
- legacy BYO-key status is hidden and saving a new Composio key fails closed;
- connect, activate, tool listing, and tool calls reject the request;
- startup never activates managed servers;
- persisted managed servers are hidden from Settings and prompts;
- browser RPC allowlists expose no managed connection methods;
- public MCP get, mutate, trust, runtime, tools, logs, and folder RPCs reject
  reserved managed server IDs;
- legacy disconnect remains a local-only cleanup path for old state and never
  contacts the provider.

Stored legacy records may remain for historical compatibility, but they are
inert and cannot become agent tools.

## Dev Tools

Dev Tools > Shadow Agent > App access exposes a read-only runtime debugger. An
explicit probe calls `list_apps` and reports supported browser inventory,
process identity, bounded window-title diagnostics, selection state, and a
receipt hash. The probe does not navigate, capture screenshots, or read page
content.

Background Understanding detail shows the exact Gmail action names, transport,
OAuth-fallback flag, browser identity, accessibility snapshot hash, parse
status, and cleanup result. Responsive master/detail behavior must preserve
Back and Escape at narrow widths.

## Release Invariants

1. First-party app access never starts a provider OAuth flow.
2. There is no provider API or managed-MCP fallback.
3. Computer Use permission never grants source consent.
4. Source consent never grants mutation authority.
5. Every state-changing Computer Use action still passes through capability
   policy.
6. The Mac lock boundary fails closed.
7. Background readers use fixed action compilers and bounded extraction.
8. Raw private source snapshots are not persisted in debugging or learning
   ledgers.
9. Legacy managed connector state cannot enter prompts or runtime tools.
10. Every visible readiness and debugger control is backed by live backend
    state.
