# Onboarding Components

Sub-components used by the onboarding steps.

## Components

### `AgentChoice.tsx`

Lets the user pick which signed-in assistant answers first (Claude Code or Codex) and whether the other one steps in as backup. Applying records the order (`SetManagedAccountOrder`) and the subscription consent (`AcceptManagedModelConsent`) in one go, so the routing groups get their members and Isoform can think right away. It refreshes on the `model-accounts:updated` event and offers **Check again** (`RefreshModelAccounts`) when nothing is signed in yet. With the user's own magpie install, it shows the Isoform routing group instead of a choice. `assistantsReady(status)` is exported for the readiness rule.

**Props:**
| Prop | Type | Description |
|---|---|---|
| `onReady` | `(order: AssistantId[]) => void` (optional) | Called once Isoform can think with the applied order |

Tests: `AgentChoice.test.tsx`.

`AgentChoice` remains available to Settings. Welcome renders its compact assistant rows directly; Permissions renders seven grouped rows directly. The old toast-based PermissionsChecklist was removed.
