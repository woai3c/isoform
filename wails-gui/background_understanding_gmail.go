package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	agenttools "yansu-agent/agent/tools"
)

const (
	backgroundGmailTransport         = "computer_use"
	backgroundGmailAccessibilityRead = BackgroundGmailComputerUseRead
	backgroundGmailWindowAttempts    = 3
	backgroundGmailSnapshotAttempts  = 3
)

type backgroundComputerUseCaller interface {
	Call(ctx context.Context, name string, args any) (*CuaDriverResponse, error)
}

type backgroundComputerUseSourceLocatorCaller interface {
	CallWithSourceLocators(
		ctx context.Context,
		name string,
		args any,
	) (*CuaDriverResponse, error)
}

type backgroundGmailAuthorizer func(
	ctx context.Context,
	runID string,
	grant BackgroundSourceGrant,
	tool string,
) error

type backgroundGmailActionReceipt struct {
	Tool       string `json:"tool"`
	Outcome    string `json:"outcome"`
	DetailHash string `json:"detailHash,omitempty"`
}

type backgroundGmailObservation struct {
	ExternalID             string
	ThreadID               string
	ExternalIDHash         string
	ThreadIDHash           string
	ContentHash            string
	ProviderIdentityKind   string
	ProviderIdentityHashes []string
	AttributionKind        string
	ObservedAt             int64
	SummaryLine            string
	MetadataJSON           string
}

type backgroundGmailBatch struct {
	Query          string
	Mailbox        string
	ReceiptHash    string
	Transport      string
	BrowserBundle  string
	BrowserName    string
	BrowserPID     int
	WindowID       int64
	SnapshotID     string
	SnapshotHash   string
	AXStatus       string
	CleanupOutcome string
	SourceLocators bool
	Actions        []backgroundGmailActionReceipt
	Items          []backgroundGmailObservation
}

type backgroundGmailMissionError struct {
	err   error
	batch backgroundGmailBatch
}

func (failure *backgroundGmailMissionError) Error() string {
	if failure == nil || failure.err == nil {
		return "Gmail Computer Use mission failed"
	}
	return failure.err.Error()
}

func (failure *backgroundGmailMissionError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.err
}

func (failure *backgroundGmailMissionError) BackgroundRunFailureDetails() map[string]any {
	if failure == nil {
		return map[string]any{}
	}
	batch := failure.batch
	return map[string]any{
		"transport":     batch.Transport,
		"oauthFallback": false,
		"browser":       batch.BrowserName,
		"browserBundle": batch.BrowserBundle,
		"browserPid":    batch.BrowserPID,
		"windowId":      batch.WindowID,
		"snapshotId":    batch.SnapshotID,
		"snapshotHash":  batch.SnapshotHash,
		"axStatus":      batch.AXStatus,
		"cleanup":       batch.CleanupOutcome,
		"actions":       batch.Actions,
	}
}

type backgroundGmailReader struct {
	caller    func() (backgroundComputerUseCaller, error)
	authorize backgroundGmailAuthorizer
	wait      func(context.Context, time.Duration) error
}

func newBackgroundGmailReader(
	caller func() (backgroundComputerUseCaller, error),
	authorize backgroundGmailAuthorizer,
) *backgroundGmailReader {
	return &backgroundGmailReader{
		caller:    caller,
		authorize: authorize,
		wait: func(ctx context.Context, duration time.Duration) error {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

func (reader *backgroundGmailReader) call(
	ctx context.Context,
	runID string,
	grant BackgroundSourceGrant,
	caller backgroundComputerUseCaller,
	tool string,
	args any,
	out any,
) (backgroundGmailActionReceipt, error) {
	if reader == nil || reader.authorize == nil {
		return backgroundGmailActionReceipt{Tool: tool, Outcome: "denied"},
			errors.New("Gmail Computer Use authority gateway is unavailable")
	}
	if err := reader.authorize(ctx, runID, grant, tool); err != nil {
		return backgroundGmailActionReceipt{Tool: tool, Outcome: "denied"}, err
	}
	return backgroundGmailCall(ctx, caller, tool, args, out)
}

func (reader *backgroundGmailReader) callWithSourceLocators(
	ctx context.Context,
	runID string,
	grant BackgroundSourceGrant,
	caller backgroundComputerUseCaller,
	tool string,
	args any,
	out any,
) (backgroundGmailActionReceipt, error) {
	if reader == nil || reader.authorize == nil {
		return backgroundGmailActionReceipt{Tool: tool, Outcome: "denied"},
			errors.New("Gmail Computer Use authority gateway is unavailable")
	}
	if err := reader.authorize(ctx, runID, grant, tool); err != nil {
		return backgroundGmailActionReceipt{Tool: tool, Outcome: "denied"}, err
	}
	return backgroundGmailCallWithSourceLocators(ctx, caller, tool, args, out)
}

func backgroundGrantAllowsOnlyGmailComputerUseRead(
	grant BackgroundSourceGrant,
) bool {
	return grant.Source == BackgroundSourceGmail &&
		grant.Status == "active" &&
		len(grant.AllowedOperations) == 1 &&
		grant.AllowedOperations[0] == backgroundGmailAccessibilityRead
}

func backgroundGmailQuery(
	grant BackgroundSourceGrant,
	now time.Time,
) string {
	cutoff := now.UTC().
		AddDate(0, 0, -grant.LookbackDays).
		Format("2006/01/02")
	query := strings.TrimSpace(grant.SourceQuery)
	if query == "" {
		return "after:" + cutoff
	}
	return "after:" + cutoff + " " + query
}

func backgroundGmailSearchURL(query, mailbox string) string {
	encoded := url.QueryEscape(strings.TrimSpace(query))
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	account := strings.TrimSpace(mailbox)
	if account == "" {
		account = "0"
	}
	return "https://mail.google.com/mail/u/" + url.PathEscape(account) +
		"/#search/" + encoded
}

type backgroundCUAApp struct {
	Bundle   string `json:"bundle"`
	Name     string `json:"name"`
	PID      int    `json:"pid"`
	Active   bool   `json:"active"`
	Running  bool   `json:"running"`
	UseCount int    `json:"use_count"`
	Windows  []struct {
		Title     string `json:"title"`
		Focused   bool   `json:"focused"`
		Minimized bool   `json:"minimized"`
	} `json:"windows"`
}

type backgroundCUAWindow struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Focused   bool   `json:"focused"`
	Minimized bool   `json:"minimized"`
}

type backgroundCUAElement struct {
	Ref           string `json:"ref"`
	Role          string `json:"role"`
	Title         string `json:"title"`
	Value         string `json:"value"`
	Description   string `json:"description"`
	URL           string `json:"url"`
	ChildrenCount int    `json:"children_count"`
	Bounds        *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		W float64 `json:"w"`
		H float64 `json:"h"`
	} `json:"bounds"`
}

type backgroundCUASnapshot struct {
	SnapshotID string                 `json:"snapshot_id"`
	AXStatus   string                 `json:"ax_status"`
	App        backgroundCUAApp       `json:"app"`
	Window     backgroundCUAWindow    `json:"window"`
	Elements   []backgroundCUAElement `json:"ui_elements"`
}

func backgroundGmailCall(
	ctx context.Context,
	caller backgroundComputerUseCaller,
	tool string,
	args any,
	out any,
) (backgroundGmailActionReceipt, error) {
	response, err := caller.Call(ctx, tool, args)
	return backgroundGmailDecodeCall(response, err, tool, out)
}

func backgroundGmailCallWithSourceLocators(
	ctx context.Context,
	caller backgroundComputerUseCaller,
	tool string,
	args any,
	out any,
) (backgroundGmailActionReceipt, error) {
	sensitive, ok := caller.(backgroundComputerUseSourceLocatorCaller)
	if !ok {
		return backgroundGmailActionReceipt{Tool: tool, Outcome: "denied"},
			errors.New("Computer Use source-locator capability is unavailable")
	}
	response, err := sensitive.CallWithSourceLocators(ctx, tool, args)
	return backgroundGmailDecodeCall(response, err, tool, out)
}

func backgroundGmailDecodeCall(
	response *CuaDriverResponse,
	err error,
	tool string,
	out any,
) (backgroundGmailActionReceipt, error) {
	if err != nil {
		return backgroundGmailActionReceipt{Tool: tool, Outcome: "failed"}, err
	}
	if response == nil {
		return backgroundGmailActionReceipt{Tool: tool, Outcome: "failed"},
			errors.New("Computer Use returned no response")
	}
	if !response.OK {
		return backgroundGmailActionReceipt{Tool: tool, Outcome: "failed"},
			errors.New(response.ErrorMessage())
	}
	receipt := backgroundGmailActionReceipt{
		Tool:       tool,
		Outcome:    "completed",
		DetailHash: agencyPayloadHash(response.Result),
	}
	if out != nil {
		if err := json.Unmarshal(response.Result, out); err != nil {
			return backgroundGmailActionReceipt{Tool: tool, Outcome: "failed"},
				fmt.Errorf("decode Computer Use %s result: %w", tool, err)
		}
	}
	return receipt, nil
}

const backgroundGmailProviderIdentityKind = "gmail_thread_locator_sha256"

func backgroundGmailCanonicalProviderLocator(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") ||
		!strings.EqualFold(parsed.Hostname(), "mail.google.com") {
		return "", false
	}
	pathParts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(pathParts) < 3 || pathParts[0] != "mail" || pathParts[1] != "u" ||
		strings.TrimSpace(pathParts[2]) == "" {
		return "", false
	}
	fragmentParts := strings.Split(strings.Trim(parsed.Fragment, "/"), "/")
	if len(fragmentParts) < 2 {
		return "", false
	}
	route := strings.ToLower(strings.TrimSpace(fragmentParts[0]))
	switch route {
	case "sent", "drafts", "inbox", "all", "label":
	case "search":
		if len(fragmentParts) < 3 {
			return "", false
		}
	default:
		return "", false
	}
	candidate, err := url.PathUnescape(fragmentParts[len(fragmentParts)-1])
	if err != nil {
		return "", false
	}
	candidate = strings.TrimSpace(candidate)
	if len(candidate) < 8 || len(candidate) > 128 {
		return "", false
	}
	for _, character := range candidate {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' {
			continue
		}
		return "", false
	}
	return "gmail-thread:" + candidate, true
}

type backgroundGmailRow struct {
	Text                   string
	Y                      float64
	ProviderIdentityHashes []string
}

func backgroundGmailStringContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func backgroundSelectBrowser(apps []backgroundCUAApp) backgroundCUAApp {
	best := backgroundCUAApp{}
	bestScore := -1
	for _, app := range apps {
		if !computerUseBrowserBundle(app.Bundle) {
			continue
		}
		score := app.UseCount
		if app.Running {
			score += 1_000
		}
		if app.Active {
			score += 100
		}
		for _, window := range app.Windows {
			lower := strings.ToLower(window.Title)
			if strings.Contains(lower, "gmail") ||
				strings.Contains(lower, "inbox") && strings.Contains(lower, "google") {
				score += 10_000
			}
		}
		if score > bestScore {
			best = app
			bestScore = score
		}
	}
	if best.Bundle == "" && len(computerUseBrowserCatalog) > 0 {
		best.Bundle = computerUseBrowserCatalog[0].Bundle
		best.Name = computerUseBrowserCatalog[0].Name
	}
	return best
}

func backgroundSelectGmailWindow(windows []backgroundCUAWindow) backgroundCUAWindow {
	best := backgroundCUAWindow{}
	bestScore := -1
	for _, window := range windows {
		score := 0
		lower := strings.ToLower(window.Title)
		if strings.Contains(lower, "gmail") {
			score += 1_000
		} else if strings.Contains(lower, "accounts.google.com") ||
			strings.Contains(lower, "sign in") && strings.Contains(lower, "google") {
			score += 500
		} else {
			continue
		}
		if strings.Contains(lower, "inbox") {
			score += 100
		}
		if window.Focused {
			score += 10
		}
		if !window.Minimized {
			score++
		}
		if score > bestScore {
			best = window
			bestScore = score
		}
	}
	return best
}

// backgroundGmailPageSnapshot keeps, of the Gmail window's snapshot, only
// the page: its web areas and what lies under them, as an unattended page
// read keeps its approved pages (agenttools.CuaWebPageElements). The tab
// strip (other tabs' titles), the address bar, bookmarks, side panels and
// anything drawn over the page never become rows. With source locators
// every page must be on Gmail. Without them (the default, which never widens
// a snapshot to addresses) the page is taken to be Gmail because the reader
// opened the Gmail search in the front tab just before, the window's front
// tab is titled Gmail, and the page itself shows the exact granted query;
// the caller checks the last two.
func backgroundGmailPageSnapshot(raw json.RawMessage, sourceLocators bool) (backgroundCUASnapshot, error) {
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return backgroundCUASnapshot{}, fmt.Errorf("decode Computer Use get_window_state result: %w", err)
	}
	elements, _ := fields["ui_elements"].([]any)
	kept, pages, _, ok := agenttools.CuaWebPageElements(elements)
	if !ok {
		return backgroundCUASnapshot{}, errors.New("Gmail did not expose a web page; no rows were read")
	}
	if sourceLocators {
		for _, page := range pages {
			address, _ := page["url"].(string)
			parsed, err := url.Parse(strings.TrimSpace(address))
			if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "mail.google.com") {
				return backgroundCUASnapshot{}, errors.New("the browser window shows a page other than Gmail; no rows were read")
			}
		}
	}
	fields["ui_elements"] = kept
	trimmed, err := json.Marshal(fields)
	if err != nil {
		return backgroundCUASnapshot{}, err
	}
	var snapshot backgroundCUASnapshot
	if err := json.Unmarshal(trimmed, &snapshot); err != nil {
		return backgroundCUASnapshot{}, fmt.Errorf("decode Computer Use get_window_state result: %w", err)
	}
	return snapshot, nil
}

func backgroundGmailWindowLooksLikeGmail(title string) bool {
	lower := strings.ToLower(strings.TrimSpace(title))
	return strings.Contains(lower, "gmail") ||
		strings.Contains(lower, "mail.google.com")
}

func backgroundGmailSnapshotLooksSignedOut(snapshot backgroundCUASnapshot) bool {
	var text strings.Builder
	text.WriteString(snapshot.Window.Title)
	for _, element := range snapshot.Elements {
		text.WriteByte(' ')
		text.WriteString(element.Title)
		text.WriteByte(' ')
		text.WriteString(element.Value)
		text.WriteByte(' ')
		text.WriteString(element.Description)
	}
	lower := strings.ToLower(text.String())
	return strings.Contains(lower, "accounts.google.com") ||
		strings.Contains(lower, "sign in to continue to gmail") ||
		strings.Contains(lower, "sign in") &&
			strings.Contains(lower, "create account")
}

func backgroundGmailSnapshotMatchesQuery(
	snapshot backgroundCUASnapshot,
	query string,
) bool {
	want := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if want == "" {
		return false
	}
	for _, element := range snapshot.Elements {
		text := strings.ToLower(strings.Join(
			strings.Fields(backgroundGmailElementText(element)),
			" ",
		))
		if strings.Contains(text, want) {
			return true
		}
	}
	return false
}

func backgroundGmailElementText(element backgroundCUAElement) string {
	parts := []string{}
	seen := map[string]bool{}
	for _, raw := range []string{element.Title, element.Value, element.Description} {
		value := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		parts = append(parts, value)
	}
	return strings.Join(parts, " | ")
}

func backgroundGmailRowTextUseful(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 16 {
		return false
	}
	lower := strings.ToLower(value)
	for _, excluded := range []string{
		"compose", "primary", "promotions", "social", "settings",
		"google apps", "search mail", "support", "terms", "privacy",
		"select all", "refresh", "more", "inbox sent drafts spam trash",
	} {
		if lower == excluded {
			return false
		}
	}
	return true
}

func backgroundGmailRows(snapshot backgroundCUASnapshot, limit int) []backgroundGmailRow {
	type row struct {
		Y                      float64
		Parts                  []string
		ProviderIdentityHashes []string
	}
	rows := map[int]*row{}
	for _, element := range snapshot.Elements {
		if element.Bounds == nil || element.Bounds.W <= 0 || element.Bounds.H <= 0 {
			continue
		}
		center := element.Bounds.Y + element.Bounds.H/2
		bucket := int(center / 6)
		entry := rows[bucket]
		if entry == nil {
			entry = &row{Y: center}
			rows[bucket] = entry
		}
		if locator, ok := backgroundGmailCanonicalProviderLocator(element.URL); ok {
			hash := agencyPayloadHash([]byte(locator))
			if !backgroundGmailStringContains(entry.ProviderIdentityHashes, hash) {
				entry.ProviderIdentityHashes = append(entry.ProviderIdentityHashes, hash)
			}
		}
		text := backgroundGmailElementText(element)
		if !backgroundGmailRowTextUseful(text) {
			continue
		}
		lowerRole := strings.ToLower(element.Role)
		if !strings.Contains(lowerRole, "statictext") &&
			!strings.Contains(lowerRole, "row") &&
			!strings.Contains(lowerRole, "cell") &&
			!strings.Contains(lowerRole, "link") &&
			!strings.Contains(lowerRole, "button") &&
			!strings.Contains(lowerRole, "group") {
			continue
		}
		duplicate := false
		for index, existing := range entry.Parts {
			if existing == text || strings.Contains(existing, text) {
				duplicate = true
				break
			}
			if strings.Contains(text, existing) {
				entry.Parts[index] = text
				duplicate = true
				break
			}
		}
		if !duplicate {
			entry.Parts = append(entry.Parts, text)
		}
	}
	ordered := make([]row, 0, len(rows))
	for _, entry := range rows {
		ordered = append(ordered, *entry)
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Y < ordered[j].Y })
	result := make([]backgroundGmailRow, 0, limit)
	seen := map[string]bool{}
	for _, entry := range ordered {
		text := agencyBoundedText(strings.Join(entry.Parts, " | "), 1_200)
		if !backgroundGmailRowTextUseful(text) {
			continue
		}
		key := agencyPayloadHash([]byte(strings.ToLower(text)))
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, backgroundGmailRow{
			Text:                   text,
			Y:                      entry.Y,
			ProviderIdentityHashes: append([]string(nil), entry.ProviderIdentityHashes...),
		})
		if len(result) >= limit {
			break
		}
	}
	return result
}

func backgroundGmailObservationsFromSnapshot(
	snapshot backgroundCUASnapshot,
	mailbox string,
	limit int,
	now time.Time,
) []backgroundGmailObservation {
	rows := backgroundGmailRows(snapshot, limit)
	items := make([]backgroundGmailObservation, 0, len(rows))
	mailbox = strings.ToLower(strings.TrimSpace(mailbox))
	for _, row := range rows {
		canonical := strings.Join(strings.Fields(row.Text), " ")
		contentHash := agencyPayloadHash([]byte(canonical))
		attribution := "other"
		if mailbox != "" && strings.Contains(strings.ToLower(canonical), mailbox) {
			attribution = "self"
		}
		tag := strings.ToUpper(attribution)
		metadata := backgroundJSON(map[string]any{
			"transport":    backgroundGmailTransport,
			"snapshotId":   snapshot.SnapshotID,
			"snapshotHash": agencyPayloadHash([]byte(snapshot.SnapshotID + "\x00" + snapshot.AXStatus)),
			"axStatus":     snapshot.AXStatus,
			"rowY":         row.Y,
			"textLength":   len(canonical),
		}, "{}")
		items = append(items, backgroundGmailObservation{
			ExternalID:             contentHash,
			ExternalIDHash:         agencyPayloadHash([]byte("gmail-row\x00" + canonical)),
			ContentHash:            contentHash,
			ProviderIdentityKind:   backgroundGmailProviderIdentityKind,
			ProviderIdentityHashes: append([]string(nil), row.ProviderIdentityHashes...),
			AttributionKind:        attribution,
			ObservedAt:             now.UnixMilli(),
			SummaryLine:            "[" + tag + "] Gmail row: " + canonical,
			MetadataJSON:           metadata,
		})
	}
	return items
}

func (reader *backgroundGmailReader) Read(
	ctx context.Context,
	runID string,
	grant BackgroundSourceGrant,
	mailbox string,
	now time.Time,
) (backgroundGmailBatch, error) {
	return reader.readQuery(
		ctx,
		runID,
		grant,
		mailbox,
		backgroundGmailQuery(grant, now),
		grant.MaxItemsPerRun,
		now,
	)
}

// readQuery executes the fixed Gmail Computer Use read sequence for a query
// already compiled by trusted backend code. Callers must derive the query from
// the canonical source grant and may only narrow that grant's scope.
func (reader *backgroundGmailReader) readQuery(
	ctx context.Context,
	runID string,
	grant BackgroundSourceGrant,
	mailbox string,
	query string,
	itemLimit int,
	now time.Time,
) (backgroundGmailBatch, error) {
	return reader.readQueryWithSourceLocatorMode(
		ctx, runID, grant, mailbox, query, itemLimit, now, false,
	)
}

func (reader *backgroundGmailReader) readQueryWithSourceLocatorMode(
	ctx context.Context,
	runID string,
	grant BackgroundSourceGrant,
	mailbox string,
	query string,
	itemLimit int,
	now time.Time,
	includeSourceLocators bool,
) (backgroundGmailBatch, error) {
	if reader == nil || reader.caller == nil {
		return backgroundGmailBatch{}, errors.New("Gmail Computer Use reader is unavailable")
	}
	if !backgroundGrantAllowsOnlyGmailComputerUseRead(grant) {
		return backgroundGmailBatch{}, errors.New("Gmail source grant does not match the Computer Use metadata-only policy")
	}
	query = strings.Join(strings.Fields(strings.TrimSpace(query)), " ")
	if query == "" {
		return backgroundGmailBatch{}, errors.New("Gmail Computer Use query is required")
	}
	grantedQuery := backgroundGmailQuery(grant, now)
	if query != grantedQuery && !strings.HasPrefix(query, grantedQuery+" ") {
		return backgroundGmailBatch{}, errors.New(
			"Gmail Computer Use query is outside the granted source scope",
		)
	}
	if itemLimit < 1 || itemLimit > grant.MaxItemsPerRun {
		return backgroundGmailBatch{}, errors.New(
			"Gmail Computer Use item limit exceeds the granted scope",
		)
	}
	caller, err := reader.caller()
	if err != nil {
		return backgroundGmailBatch{}, err
	}
	batch := backgroundGmailBatch{
		Query:          query,
		Mailbox:        strings.ToLower(strings.TrimSpace(mailbox)),
		Transport:      backgroundGmailTransport,
		SourceLocators: includeSourceLocators,
		Actions:        []backgroundGmailActionReceipt{},
	}
	var inventory struct {
		Apps []backgroundCUAApp `json:"apps"`
	}
	receipt, err := reader.call(ctx, runID, grant, caller, "list_apps", map[string]any{"days": 14}, &inventory)
	batch.Actions = append(batch.Actions, receipt)
	if err != nil {
		return batch, fmt.Errorf("inspect signed-in browser sessions: %w", err)
	}
	browser := backgroundSelectBrowser(inventory.Apps)
	if browser.Bundle == "" {
		return batch, errors.New("no supported browser is available for Gmail Computer Use")
	}
	var launched struct {
		App backgroundCUAApp `json:"app"`
	}
	receipt, err = reader.call(ctx, runID, grant, caller, "launch_app", map[string]any{
		"bundle":    browser.Bundle,
		"activates": false,
	}, &launched)
	batch.Actions = append(batch.Actions, receipt)
	if err != nil {
		return batch, fmt.Errorf("open browser for Gmail: %w", err)
	}
	if launched.App.PID > 0 {
		browser.PID = launched.App.PID
	}
	if launched.App.Name != "" {
		browser.Name = launched.App.Name
	}
	batch.BrowserBundle = browser.Bundle
	batch.BrowserName = browser.Name
	batch.BrowserPID = browser.PID
	receipt, err = reader.call(ctx, runID, grant, caller, "open_url", map[string]any{
		"url":    backgroundGmailSearchURL(batch.Query, batch.Mailbox),
		"bundle": browser.Bundle,
	}, nil)
	batch.Actions = append(batch.Actions, receipt)
	if err != nil {
		return batch, fmt.Errorf("navigate to the granted Gmail scope: %w", err)
	}
	batch.CleanupOutcome = "not_attempted_unconfirmed_target"
	if reader.wait != nil {
		if err := reader.wait(ctx, 1500*time.Millisecond); err != nil {
			return batch, err
		}
	}
	window := backgroundCUAWindow{}
	for attempt := 0; attempt < backgroundGmailWindowAttempts; attempt++ {
		var windowsPayload struct {
			Windows []backgroundCUAWindow `json:"windows"`
		}
		receipt, err = reader.call(ctx, runID, grant, caller, "list_windows", map[string]any{
			"bundle": browser.Bundle,
		}, &windowsPayload)
		batch.Actions = append(batch.Actions, receipt)
		if err != nil {
			return batch, fmt.Errorf("locate Gmail browser window: %w", err)
		}
		window = backgroundSelectGmailWindow(windowsPayload.Windows)
		if window.ID > 0 {
			break
		}
		if attempt+1 < backgroundGmailWindowAttempts && reader.wait != nil {
			if waitErr := reader.wait(ctx, 800*time.Millisecond); waitErr != nil {
				return batch, waitErr
			}
		}
	}
	if window.ID <= 0 {
		return batch, errors.New("the bounded Gmail search did not expose a Gmail window; sign in normally and retry")
	}
	batch.WindowID = window.ID
	var snapshot backgroundCUASnapshot
	for attempt := 0; attempt < backgroundGmailSnapshotAttempts; attempt++ {
		args := map[string]any{
			"pid":                browser.PID,
			"bundle":             browser.Bundle,
			"include_screenshot": false,
			"interactive_only":   false,
			"depth":              40,
			"wakeup_seconds":     3,
		}
		if window.ID > 0 {
			args["window_id"] = window.ID
		}
		var raw json.RawMessage
		if includeSourceLocators {
			receipt, err = reader.callWithSourceLocators(
				ctx, runID, grant, caller, "get_window_state", args, &raw,
			)
		} else {
			receipt, err = reader.call(
				ctx, runID, grant, caller, "get_window_state", args, &raw,
			)
		}
		batch.Actions = append(batch.Actions, receipt)
		if err == nil {
			snapshot, err = backgroundGmailPageSnapshot(raw, includeSourceLocators)
		}
		if err == nil && len(snapshot.Elements) > 0 {
			break
		}
		if attempt+1 < backgroundGmailSnapshotAttempts && reader.wait != nil {
			if waitErr := reader.wait(ctx, 800*time.Millisecond); waitErr != nil {
				return batch, waitErr
			}
		}
	}
	if err != nil {
		return batch, fmt.Errorf("read Gmail accessibility state: %w", err)
	}
	batch.SnapshotID = snapshot.SnapshotID
	batch.AXStatus = snapshot.AXStatus
	snapshotCanonical, _ := json.Marshal(snapshot.Elements)
	batch.SnapshotHash = agencyPayloadHash(snapshotCanonical)
	if backgroundGmailSnapshotLooksSignedOut(snapshot) {
		return batch, errors.New("Gmail is not signed in in the selected browser; sign in normally and retry")
	}
	if !backgroundGmailWindowLooksLikeGmail(snapshot.Window.Title) ||
		window.ID > 0 && snapshot.Window.ID > 0 && snapshot.Window.ID != window.ID {
		return batch, errors.New("Computer Use did not confirm the bounded Gmail target window; no rows were read")
	}
	if !backgroundGmailSnapshotMatchesQuery(snapshot, batch.Query) {
		return batch, errors.New("Gmail did not expose the granted search query; no rows were read")
	}
	batch.Items = backgroundGmailObservationsFromSnapshot(
		snapshot,
		batch.Mailbox,
		itemLimit,
		now,
	)
	cleanupArgs := map[string]any{
		"pid":       browser.PID,
		"key":       "w",
		"modifiers": []string{"cmd"},
	}
	if window.ID > 0 {
		cleanupArgs["window_id"] = window.ID
	}
	cleanupReceipt, cleanupErr := reader.call(ctx, runID, grant, caller, "press_key", cleanupArgs, nil)
	batch.Actions = append(batch.Actions, cleanupReceipt)
	batch.CleanupOutcome = "completed"
	if cleanupErr != nil {
		batch.CleanupOutcome = "failed: " + agencyBoundedText(cleanupErr.Error(), 240)
	}
	receiptParts := []string{
		grant.ID,
		batch.Query,
		batch.Mailbox,
		batch.Transport,
		batch.BrowserBundle,
		batch.SnapshotHash,
	}
	for _, item := range batch.Items {
		receiptParts = append(
			receiptParts,
			item.ExternalIDHash,
			item.ContentHash,
			item.ProviderIdentityKind,
		)
		receiptParts = append(receiptParts, item.ProviderIdentityHashes...)
	}
	batch.ReceiptHash = agencyPayloadHash([]byte(strings.Join(receiptParts, "\x00")))
	return batch, nil
}

func backgroundGmailSummary(
	batch backgroundGmailBatch,
	items []backgroundGmailObservation,
) string {
	var builder strings.Builder
	builder.WriteString("[Gmail Computer Use Understanding]\n")
	if batch.Mailbox != "" {
		builder.WriteString("Bound User: ")
		builder.WriteString(batch.Mailbox)
		builder.WriteString("\n")
	} else {
		builder.WriteString("Bound User: [unknown]\n")
	}
	builder.WriteString("Transport boundary: read from the existing signed-in Gmail browser session through macOS Accessibility; no OAuth or provider API was used.\n")
	builder.WriteString("Source boundary: visible search-result metadata and snippets only; message bodies and attachments were not inspected.\n")
	builder.WriteString("Attribution boundary: [SELF] is explicitly tied to the bound user; [OTHER] is context and cannot establish a personal trait.\n\n")
	for _, item := range items {
		builder.WriteString(item.SummaryLine)
		builder.WriteString("\n")
	}
	return agencyBoundedText(builder.String(), 20_000)
}
