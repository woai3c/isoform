# client

HTTP client for the Yansu API, used by the yansu-agent to interact with the backend platform.

## Overview

The `client` package provides a typed Go HTTP client (`Client`) that wraps all Yansu REST API calls. It handles authentication, request building, response decoding (including multiple paginated response envelope shapes), and error handling.

## Key Components

### `api.go`

The single file containing all types and methods for the client.

**Client struct**

```go
c := client.New(baseURL, token)
c.SetProject(org, product, project)  // scope subsequent requests
c.SetProductUUID(uuid)               // scope knowledge queries
```

- `baseURL` — e.g. `https://app.yansu.ai`
- `token` — Bearer token for Authorization header
- Default HTTP timeout: 30 seconds

## API Surface

Methods are grouped by domain:

### Auth / Profile

| Method | Endpoint | Description |
|--------|----------|-------------|
| `Verify(ctx)` | `GET /api/auth/profile` | Validate the configured token |
| `GetProfile(ctx)` | `GET /api/auth/profile` | Return `UserProfile` for the authenticated user |

### Organizations, Products & Projects

| Method | Description |
|--------|-------------|
| `ListOrganizations(ctx)` | All orgs the user belongs to |
| `ListProducts(ctx, org)` | All products in an org |
| `ListProjectsInProduct(ctx, org, product)` | All projects in a product |
| `GetProject(ctx, org, product, project)` | Single project by slug path |
| `GetProjectCodeRepositories(ctx, org, product, project)` | Code repos linked to a project |
| `GetProjectContext(ctx)` | PRD and scenario content for the configured project |

### Knowledge

| Method | Description |
|--------|-------------|
| `ListKnowledge(ctx)` | All knowledge items for the configured product |
| `CreateKnowledge(ctx, req)` | Create a new knowledge item |
| `UpdateKnowledge(ctx, uuid, req)` | Update an existing knowledge item |
| `DeleteKnowledge(ctx, uuid)` | Delete a knowledge item |
| `ExtractKnowledge(ctx, req)` | AI-extract knowledge from a git diff/commit |
| `ListSkills(ctx)` | All skills for the configured product |

### Stages (SOP Workflow)

| Method | Description |
|--------|-------------|
| `ListStages(ctx)` | All stages for the configured project (summary mode) |
| `GetStage(ctx, stageName)` | Full stage details including content |
| `RunStage(ctx, stageName)` | Trigger stage execution |
| `UpdateStage(ctx, stageName, req)` | Update stage content (creates a new minor version) |
| `GetStageTasksSummary(ctx, stageName)` | Executor task counts for a stage |
| `ListStageVersions(ctx, stageName)` | Version history for a stage |
| `RestoreStageVersion(ctx, stageName, versionNumber)` | Roll back to a previous version |

### Feedback

| Method | Description |
|--------|-------------|
| `ListFeedbacksByStage(ctx, stageName)` | Feedbacks across all versions of a stage |
| `ListPendingReplyFeedbacks(ctx)` | Feedbacks awaiting the current user's reply |
| `GetFeedback(ctx, feedbackUUID)` | Single feedback by UUID |
| `UpdateFeedback(ctx, feedbackUUID, status)` | Change feedback status (e.g. resolve) |
| `RetryFeedback(ctx, feedbackUUID)` | Re-trigger AI processing for a feedback |
| `ListFeedbackComments(ctx, feedbackUUID)` | All comments (auto-paginates, page size 200) |
| `CreateFeedbackComment(ctx, feedbackUUID, req)` | Add a comment to a feedback |

### Scenarios

| Method | Description |
|--------|-------------|
| `ListScenarios(ctx)` | All scenarios for the configured project |
| `GetScenario(ctx, uuid)` | Full scenario details including optional checklist |
| `RequeueScenario(ctx, uuid)` | Re-queue a failed or completed scenario |
| `CancelScenario(ctx, uuid)` | Cancel a queued/building scenario |
| `GenerateScenarioChecklist(ctx, uuid)` | Trigger async checklist generation for a passed scenario |
| `UpdateScenarioChecklistItem(ctx, scenarioUUID, itemID, checked)` | Toggle a checklist item |

### Daemon / Slack Integration

| Method | Description |
|--------|-------------|
| `GetMySlackBinding(ctx)` | Auto-discover `team_id` / `slack_user_id` for the current user |
| `DaemonFetchContext(ctx, relayID)` | Retrieve conversation context for a relay event |
| `DaemonVerifySlack(ctx, slackUserID)` | Initiate Slack DM verification (sends a code) |
| `DaemonConfirmSlack(ctx, slackUserID, code)` | Confirm the verification code and create binding |

## Response Decoding

The backend returns list responses in several shapes. Two internal generic helpers normalize them transparently:

- **`decodeListResponse[T]`** — tries raw array → `{data:[], total}` → `{items:[], total}` in sequence; returns `(items, total, error)`.
- **`decodeListOrPagedItems[T]`** — tries raw array → `{items:[]}` envelope.

`ExecutorTaskSummary` has a custom `UnmarshalJSON` that supports both a flat format (`{"running":1,...}`) and a nested format (`{"by_status":{"running":1,...}}`).

## Error Handling

All methods return Go-style `error` values. Non-2xx responses are decoded from:
```json
{"error": "..."}
// or
{"message": "..."}
```
and wrapped as `fmt.Errorf("API error (%d): %s", statusCode, msg)`.

## Usage

```go
import "github.com/org/repo/yansu-agent/internal/client"

c := client.New("https://app.yansu.ai", os.Getenv("YANSU_TOKEN"))
c.SetProject("my-org", "my-product", "my-project")

// Verify token
if err := c.Verify(ctx); err != nil {
    log.Fatal("invalid token:", err)
}

// List stages
stages, err := c.ListStages(ctx)

// Create knowledge
knowledge, err := c.CreateKnowledge(ctx, &client.CreateKnowledgeRequest{
    Trigger: "How to handle X",
    Content: "Always do Y because Z.",
    Scope:   "product",
})
```

## Testing

`api_test.go` contains unit tests for:

- `decodeListResponse` with all supported envelope shapes (raw array, `data` wrapper, `items` wrapper, empty, invalid JSON, null fallthrough)
- JSON round-trip validation for SOP types (`StageSummaryResponse`, `PendingReplyFeedback`, `CreateFeedbackCommentRequest`, `RestoreStageVersionRequest`)
- `ExecutorTaskSummary` deserialization for both flat and `by_status` shapes
- `ListFeedbackComments` pagination logic using `httptest.NewServer` (verifies offset increments and total-based termination)

Run tests:
```bash
cd yansu-agent && go test ./internal/client/...
```

## Dependencies

Standard library only:
- `net/http` — HTTP client
- `encoding/json` — JSON marshalling/unmarshalling
- `context` — request cancellation
- `fmt`, `io`, `bytes`, `time`
