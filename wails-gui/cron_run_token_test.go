package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
	"yansu-agent/agent/authority"
	"yansu-agent/runtoken"
)

func scopedCronTestServer(t *testing.T) (*App, *cronAPIServer) {
	t.Helper()
	rig := newHandoffTestRig(t)
	app := rig.app
	app.ctx = context.Background()
	app.cronScheduler = NewCronScheduler(rig.state, func(string, any) {}, func(string, string) {}, nil)
	schedulerCtx, stop := context.WithCancel(app.ctx)
	go app.cronScheduler.Run(schedulerCtx)
	select {
	case <-app.cronScheduler.loadedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not load")
	}
	t.Cleanup(func() { stop(); app.cronScheduler.Stop() })
	api, err := newCronAPIServer(app)
	if err != nil {
		t.Fatal(err)
	}
	app.cronAPI = api
	t.Cleanup(api.Stop)
	return app, api
}

func scopedCronRequest(t *testing.T, api *cronAPIServer, token, method, path, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", api.Port(), path), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}

func TestScopedCronAPIEnforcesEndpointWorkspaceAndOwnership(t *testing.T) {
	app, api := scopedCronTestServer(t)
	workspace := t.TempDir()
	ctx, end, err := app.issueAgentRunScope(context.Background(), &agent.TaskInput{HistoryKey: "chat-one", WorkDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	credential, _ := runtoken.FromContext(ctx)
	if credential.Token == api.Secret() {
		t.Fatal("run was given master credential")
	}
	body := `{"name":"owned","scheduleKind":"manual","scheduleConfig":{},"targetKind":"agent","targetConfig":{"prompt":"report"}}`
	status, raw := scopedCronRequest(t, api, credential.Token, "POST", "/cron/jobs", body)
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", status, raw)
	}
	var owned CronJobState
	if err := json.Unmarshal(raw, &owned); err != nil {
		t.Fatal(err)
	}
	if owned.SourceSessionID != "chat-one" {
		t.Fatal("owner was taken from active-session file")
	}
	if status, _ := scopedCronRequest(t, api, credential.Token, "GET", "/cron/jobs/"+owned.ID, ""); status != 200 {
		t.Fatal("owner could not read its own job")
	}
	otherCtx, otherEnd, err := app.issueAgentRunScope(context.Background(), &agent.TaskInput{HistoryKey: "chat-two", WorkDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer otherEnd()
	other, _ := runtoken.FromContext(otherCtx)
	if status, raw := scopedCronRequest(t, api, other.Token, "GET", "/cron/jobs", ""); status != 200 || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatal("another session listed owned jobs")
	}
	if status, _ := scopedCronRequest(t, api, other.Token, "GET", "/cron/jobs/"+owned.ID, ""); status != 403 {
		t.Fatal("guessed job ID exposed another owner's job")
	}
	wrongCtx, wrongEnd, err := app.issueAgentRunScope(context.Background(), &agent.TaskInput{HistoryKey: "chat-one", WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer wrongEnd()
	wrong, _ := runtoken.FromContext(wrongCtx)
	if status, _ := scopedCronRequest(t, api, wrong.Token, "GET", "/cron/jobs/"+owned.ID, ""); status != 403 {
		t.Fatal("same session escaped workspace scope")
	}
	for _, test := range []struct{ method, path string }{
		{"GET", "/mcp"}, {"POST", "/mcp/install"}, {"POST", "/skills/install"},
		{"PUT", "/cron/jobs/" + owned.ID}, {"DELETE", "/cron/jobs/" + owned.ID}, {"POST", "/cron/jobs/" + owned.ID + "/run"},
	} {
		if status, _ := scopedCronRequest(t, api, credential.Token, test.method, test.path, `{}`); status != 403 {
			t.Fatalf("out-of-scope %s %s returned %d", test.method, test.path, status)
		}
	}
	for _, target := range []string{
		`{"prompt":"x","project_dir":` + fmt.Sprintf("%q", t.TempDir()) + `}`,
		`{"prompt":"x","sop_id":"approved-playbook"}`, `{"prompt":"x","handoff_id":"other"}`,
		`{"prompt":"x","session_tag":"other"}`, `{"prompt":"x","future_authority_field":"yes"}`,
	} {
		attack := `{"name":"escape","scheduleKind":"manual","scheduleConfig":{},"targetKind":"agent","targetConfig":` + target + `}`
		if status, _ := scopedCronRequest(t, api, credential.Token, "POST", "/cron/jobs", attack); status != 403 {
			t.Fatalf("target scope escape returned %d", status)
		}
	}
	for _, injected := range []string{`"sourceSessionId":"other",`, `"sourcePlatform":"slack",`, `"surfaceKind":"handoff",`} {
		if status, _ := scopedCronRequest(t, api, credential.Token, "POST", "/cron/jobs", "{"+injected+body[1:]); status != 403 {
			t.Fatal("caller-controlled ownership accepted")
		}
	}
	if status, _ := scopedCronRequest(t, api, api.Secret(), "GET", "/cron/jobs", ""); status != 200 {
		t.Fatal("trusted master access changed")
	}
	end()
	if status, _ := scopedCronRequest(t, api, credential.Token, "GET", "/cron/jobs", ""); status != 401 {
		t.Fatal("token survived run end")
	}
	if status, _ := scopedCronRequest(t, api, other.Token, "GET", "/cron/jobs", ""); status != 200 {
		t.Fatal("ending one run revoked another")
	}
}

type revokingCronBody struct {
	io.Reader
	revoke func()
}

func (r *revokingCronBody) Read(p []byte) (int, error) { r.revoke(); return r.Reader.Read(p) }

func TestScopedCronRequestCannotCreateAfterRunEndsDuringBodyRead(t *testing.T) {
	app, api := scopedCronTestServer(t)
	ctx, end, err := app.issueAgentRunScope(context.Background(), &agent.TaskInput{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	credential, _ := runtoken.FromContext(ctx)
	body := &revokingCronBody{Reader: strings.NewReader(`{"name":"late","scheduleKind":"manual","scheduleConfig":{},"targetKind":"agent","targetConfig":{"prompt":"late"}}`), revoke: end}
	req := httptest.NewRequest(http.MethodPost, "/cron/jobs", body)
	req.Header.Set("Authorization", "Bearer "+credential.Token)
	response := httptest.NewRecorder()
	api.auth(api.handleJobs)(response, req)
	if response.Code != http.StatusUnauthorized || len(app.ListCronJobs()) != 0 {
		t.Fatal("request outlived run and created work")
	}
}

func TestTextOnlyRunCanStartBeforeSchedulerAPI(t *testing.T) {
	app := &App{}
	ctx, end, err := app.issueAgentRunScope(context.Background(), &agent.TaskInput{NoTools: true, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	credential, _ := runtoken.FromContext(ctx)
	scope, err := app.runTokens.Verify(credential.Token)
	if err != nil || credential.Port != 0 || len(scope.Endpoints) != 0 {
		t.Fatal("pre-API text run gained API permissions or failed issuance")
	}
	if _, added := ctx.Deadline(); added {
		t.Fatal("credential expiry replaced runner default timeout rules")
	}
	if scope.ExpiresAt.IsZero() || scope.ExpiresAt.After(time.Now().Add(executeTimeout)) {
		t.Fatal("unbounded credential TTL")
	}
}

func TestScopedCronDuplicateDoesNotDiscloseAnotherSessionsJob(t *testing.T) {
	app, api := scopedCronTestServer(t)
	// Match the API's canonical stored target even when TMPDIR is an alias.
	workspace, err := runtoken.Workspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, end, err := app.issueAgentRunScope(context.Background(), &agent.TaskInput{WorkDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	credential, _ := runtoken.FromContext(ctx)
	target, _ := json.Marshal(cronTargetConfig{Prompt: "same task", ProjectDir: workspace})
	row := storage.CronJobRow{ID: "private-other-job", Name: "private other title", TargetKind: "agent", TargetConfig: string(target),
		SourceSessionID: "other-chat", ScheduleKind: "manual", ScheduleConfig: `{}`, Fingerprint: computeAutomationFingerprint("", "agent", string(target))}
	if err := app.store.SaveCronJob(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	status, raw := scopedCronRequest(t, api, credential.Token, "POST", "/cron/jobs", `{"name":"mine","scheduleKind":"manual","scheduleConfig":{},"targetKind":"agent","targetConfig":{"prompt":"same task"}}`)
	if status != http.StatusConflict || strings.Contains(string(raw), row.ID) || strings.Contains(string(raw), row.Name) {
		t.Fatalf("duplicate disclosed another job: status=%d body=%s", status, raw)
	}
}

func TestScopedCronAPIExpiryCancellationAndNoTools(t *testing.T) {
	app, api := scopedCronTestServer(t)
	deadline, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ctx, end, err := app.issueAgentRunScope(deadline, &agent.TaskInput{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	credential, _ := runtoken.FromContext(ctx)
	<-deadline.Done()
	if status, _ := scopedCronRequest(t, api, credential.Token, "GET", "/cron/jobs", ""); status != 401 {
		t.Fatal("expired token accepted")
	}
	ctx, endNoTools, err := app.issueAgentRunScope(context.Background(), &agent.TaskInput{WorkDir: t.TempDir(), NoTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer endNoTools()
	credential, _ = runtoken.FromContext(ctx)
	if status, _ := scopedCronRequest(t, api, credential.Token, "GET", "/memory/list", ""); status != 403 {
		t.Fatal("text-only run gained API reach")
	}
	parent, stop := context.WithCancel(context.Background())
	ctx, release, err := app.issueAgentRunScope(parent, &agent.TaskInput{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	credential, _ = runtoken.FromContext(ctx)
	stop()
	if status, _ := scopedCronRequest(t, api, credential.Token, "GET", "/cron/jobs", ""); status != 401 {
		t.Fatal("cancelled token accepted")
	}
}

func TestScopedCronAPIHonorsRunAuthorityAndPrivacy(t *testing.T) {
	app, api := scopedCronTestServer(t)
	workspace := t.TempDir()
	input := &agent.TaskInput{WorkDir: workspace, AutoApproveTools: true, AuthorityGrants: []authority.Grant{{ID: "approved-read", Capability: "activity.read", ResourcePrefix: workspace}}}
	ctx, end, err := app.issueAgentRunScope(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	credential, _ := runtoken.FromContext(ctx)
	insertTestSession(t, app.activity.store, "protected-scope", "protected", 100000)
	if status, raw := scopedCronRequest(t, api, credential.Token, "GET", "/activity/sessions/protected-scope", ""); status != 404 || strings.Contains(string(raw), "typed text") {
		t.Fatalf("read privacy bypass: status=%d", status)
	}
	for _, path := range []string{"/memory/list", "/cron/jobs"} {
		if status, _ := scopedCronRequest(t, api, credential.Token, "GET", path, ""); status != 403 {
			t.Fatal("grant for another capability accepted")
		}
	}
	if status, _ := scopedCronRequest(t, api, credential.Token, "POST", "/cron/jobs", `{}`); status != 403 {
		t.Fatal("read grant became scheduling authority")
	}
	// Mutating the caller's grants after minting cannot widen the token.
	input.AuthorityGrants[0].Capability = "memory.read"
	if status, _ := scopedCronRequest(t, api, credential.Token, "GET", "/memory/list", ""); status != 403 {
		t.Fatal("issuer grant slice was mutable")
	}
	input.AuthorityGrants = []authority.Grant{{ID: "expired", Capability: "memory.read", ExpiresAt: time.Now().Add(-time.Second)}}
	expiredCtx, expiredEnd, err := app.issueAgentRunScope(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer expiredEnd()
	expired, _ := runtoken.FromContext(expiredCtx)
	if status, _ := scopedCronRequest(t, api, expired.Token, "GET", "/memory/list", ""); status != 403 {
		t.Fatal("expired authority grant accepted")
	}
}

func TestScopedCronAPICannotExpandTaskToolOrShadowScope(t *testing.T) {
	app, api := scopedCronTestServer(t)
	for _, test := range []struct {
		name         string
		edit         func(*agent.TaskInput)
		method, path string
	}{
		{"text only", func(in *agent.TaskInput) { in.NoTools = true }, "GET", "/memory/list"},
		{"plan mode", func(in *agent.TaskInput) { in.PlanMode = true }, "GET", "/memory/list"},
		{"non-shell tools", func(in *agent.TaskInput) { in.AllowedTools = []string{"Read"} }, "GET", "/memory/list"},
		{"disabled shell", func(in *agent.TaskInput) { in.DisabledTools = []string{"Bash"} }, "GET", "/memory/list"},
		{"shadow mode", func(in *agent.TaskInput) { in.ExecutionMode = authority.ExecutionModeShadow }, "POST", "/cron/jobs"},
		{"shadow identity", func(in *agent.TaskInput) { in.AuthorityShadowRunID = "shadow" }, "POST", "/cron/jobs"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := &agent.TaskInput{WorkDir: t.TempDir()}
			test.edit(input)
			ctx, end, err := app.issueAgentRunScope(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			defer end()
			credential, _ := runtoken.FromContext(ctx)
			if status, _ := scopedCronRequest(t, api, credential.Token, test.method, test.path, `{}`); status != 403 {
				t.Fatalf("task scope expanded: status=%d", status)
			}
		})
	}
}
