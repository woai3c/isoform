package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sync/singleflight"
)

// subscriptionStatusTTL is how long a GetSubscriptionStatus result stays
// in the in-process cache. Two motivations:
//   - The spotlight pre-flight at executePrompt + agentRunAllowedForSpotlight
//     issued two HTTP calls per Cmd-Space-Enter; the second hit a
//     definitely-fresh-from-the-first answer microseconds ago. The cache
//     dedupes that into one request.
//   - It narrows the TOCTOU race where the helper allows but executePrompt
//     re-checks and blocks (or vice versa). Within the TTL window both
//     reads see the same value, so a stable answer is observed.
//
// 5s is small enough that a real subscription change (Stripe webhook
// arriving while the app is open, manual plan change) becomes visible
// within a few seconds, and large enough to comfortably span the
// frontend's render cycle between a route navigation and a paywall
// fetch. Explicit invalidation is wired into every in-app code path
// that mutates plan or identity state: ChangePlan, OpenCheckout,
// CancelPendingDowngrade, completeLogin, Logout.
const subscriptionStatusTTL = 5 * time.Second

type subscriptionStatusCacheEntry struct {
	sub *SubscriptionStatus
	at  time.Time
}

var (
	subscriptionStatusCacheMu sync.Mutex
	subscriptionStatusCache   *subscriptionStatusCacheEntry
	// subscriptionStatusEpoch advances every time the cache is invalidated
	// (Logout / completeLogin / ChangePlan / OpenCheckout /
	// CancelPendingDowngrade). The singleflight callback snapshots this
	// value before issuing its HTTP request and refuses to write back
	// to the cache if the epoch has advanced — closing the race where
	// an in-flight fetch started under the old token completes AFTER an
	// explicit invalidate and would otherwise poison the cache with
	// pre-invalidate data for up to subscriptionStatusTTL. uint64 wraps
	// after ~10^19 invalidations; not a concern in practice.
	subscriptionStatusEpoch uint64
	// subscriptionStatusFetchGroup dedupes concurrent cache-miss fetches.
	// Without singleflight, two callers landing in tight succession (the
	// spotlight pre-flight: agentRunAllowedForSpotlight + executePrompt)
	// could BOTH see an empty cache and BOTH issue the same HTTP request
	// in parallel. With singleflight the second caller waits on the
	// first's in-flight call and sees its result, defeating the cold-
	// cache double-fetch the cache was added to prevent.
	subscriptionStatusFetchGroup singleflight.Group
)

// invalidateSubscriptionStatusCache clears the cached subscription so
// the next GetSubscriptionStatus call fetches fresh state. Three things
// happen under the cache mutex + one outside:
//   - The cache slot is cleared.
//   - The epoch counter advances so any in-flight singleflight callback
//     that already snapshotted the prior epoch will refuse to write its
//     (now stale) result back to the cache when it completes.
//   - subscriptionStatusFetchGroup.Forget releases the in-flight key so
//     callers arriving AFTER the invalidation are not deduped to the
//     stale in-flight call — they enter a fresh singleflight under the
//     new identity. Forget cannot cancel the running callback itself;
//     that's why GetSubscriptionStatus also does an identity check
//     (loadYansuToken before / after the HTTP call) and returns a
//     fallback when the token changed mid-fetch, so waiters that
//     already piled up at the old in-flight call don't receive stale
//     data either.
func invalidateSubscriptionStatusCache() {
	subscriptionStatusCacheMu.Lock()
	subscriptionStatusCache = nil
	subscriptionStatusEpoch++
	subscriptionStatusCacheMu.Unlock()
	subscriptionStatusFetchGroup.Forget("subscription-status")
}

// SubscriptionStatus represents the user's subscription state for the frontend
type SubscriptionStatus struct {
	Status               string     `json:"subscription_status"`
	Plan                 *string    `json:"subscription_plan"`
	IsActive             bool       `json:"is_active"`
	SubscriptionEndsAt   *time.Time `json:"subscription_ends_at"`
	SubscriptionCancelAt *time.Time `json:"subscription_cancel_at"`
	CanChangePlan        bool       `json:"can_change_plan"`
	BillingEnabled       bool       `json:"billing_enabled"`
	// ByoaAvailable mirrors the backend's STRIPE_BYOA_PRICE_ID being set —
	// i.e. whether the BYOA ($10) plan is purchasable. The plan picker hides
	// the BYOA card when this is false.
	ByoaAvailable bool `json:"byoa_available"`
}

// CheckoutResult represents the result of creating a hosted checkout session
type CheckoutResult struct {
	SessionID   string `json:"session_id"`
	CheckoutURL string `json:"checkout_url"`
}

// PortalResult represents the result of creating a billing portal session
type PortalResult struct {
	PortalURL string `json:"portal_url"`
}

// GetSubscriptionStatus fetches the current user's subscription status from the backend.
//
// Cached for subscriptionStatusTTL to dedupe rapid-fire calls — the
// agent pre-flight in executePrompt + agentRunAllowedForSpotlight used
// to issue two sequential HTTP requests per spotlight submission, and
// the frontend's paywall / sidebar plan-badge components query this
// repeatedly during navigation. Invalidated explicitly by code paths
// that mutate plan state (ChangePlan, OpenCheckout, CancelPendingDowngrade,
// Logout, completeLogin).
//
// Concurrent cache-miss callers are deduplicated through singleflight so
// a cold-cache burst (e.g. spotlight's two pre-flights firing within
// microseconds) issues exactly one HTTP request and all callers receive
// the same result.
func (a *App) GetSubscriptionStatus() (*SubscriptionStatus, error) {
	// There is no Yansu account, so no billing and no backend call. Return a
	// synthetic active, non-billable status so any consumer that still reads
	// this resolves to "everything unlocked, nothing to pay".
	if noYansuAccount() {
		// "private" names the no-uplink, BYOK-only edition. No frontend
		// surface shows a plan any more.
		plan := "private"
		return &SubscriptionStatus{
			Status:         "active",
			Plan:           &plan,
			IsActive:       true,
			CanChangePlan:  false,
			BillingEnabled: false,
			ByoaAvailable:  false,
		}, nil
	}

	subscriptionStatusCacheMu.Lock()
	if subscriptionStatusCache != nil && time.Since(subscriptionStatusCache.at) < subscriptionStatusTTL {
		cached := subscriptionStatusCache.sub
		subscriptionStatusCacheMu.Unlock()
		return cached, nil
	}
	subscriptionStatusCacheMu.Unlock()

	// Singleflight key is a constant — the subscription belongs to the
	// process's currently-authenticated user and there is only ever one
	// in-flight identity at a time.
	v, err, _ := subscriptionStatusFetchGroup.Do("subscription-status", func() (any, error) {
		// Re-check the cache + snapshot the epoch inside the
		// singleflight callback: a concurrent winner may have populated
		// the cache between our initial check and entering the group;
		// and the epoch snapshot is what guards against invalidations
		// that happen DURING our upcoming HTTP call.
		subscriptionStatusCacheMu.Lock()
		if subscriptionStatusCache != nil && time.Since(subscriptionStatusCache.at) < subscriptionStatusTTL {
			cached := subscriptionStatusCache.sub
			subscriptionStatusCacheMu.Unlock()
			return cached, nil
		}
		startEpoch := subscriptionStatusEpoch
		subscriptionStatusCacheMu.Unlock()

		startToken, tokenErr := loadYansuToken()
		if tokenErr != nil || startToken == "" {
			return &SubscriptionStatus{BillingEnabled: false}, nil
		}

		apiBase := loadAPIBaseURL()
		var status SubscriptionStatus
		if fetchErr := apiGet(apiBase, startToken, "/api/users/me/subscription", &status); fetchErr != nil {
			log.Printf("[subscription] failed to fetch subscription status: %v", fetchErr)
			// On error don't poison the cache — the next call retries.
			return &SubscriptionStatus{BillingEnabled: false}, fetchErr
		}

		// Identity check: re-load the token AFTER the HTTP call. If it
		// changed (Logout cleared it, or completeLogin replaced it
		// with a different user's token), the fetched status reflects
		// the prior identity and must NOT propagate — including to
		// waiters that piled up on this in-flight singleflight call.
		// The epoch fence alone would only block the CACHE write; this
		// guard blocks the return value too, so waiters get the
		// BillingEnabled:false fallback instead of stale plan data.
		endToken, endTokenErr := loadYansuToken()
		if endTokenErr != nil || endToken == "" || endToken != startToken {
			return &SubscriptionStatus{BillingEnabled: false}, nil
		}

		subscriptionStatusCacheMu.Lock()
		if subscriptionStatusEpoch == startEpoch {
			// No invalidation happened during our HTTP call; safe to
			// write back. If the epoch advanced, an identity / plan
			// change occurred between our token read and the apiGet
			// return — the result reflects pre-invalidate state and
			// must NOT be cached. The next caller will refetch under
			// the new identity.
			subscriptionStatusCache = &subscriptionStatusCacheEntry{sub: &status, at: time.Now()}
		}
		subscriptionStatusCacheMu.Unlock()
		return &status, nil
	})
	if err != nil {
		// Error was already logged in the callback; propagate the
		// best-effort fallback the callback returned.
		if sub, ok := v.(*SubscriptionStatus); ok && sub != nil {
			return sub, err
		}
		return &SubscriptionStatus{BillingEnabled: false}, err
	}
	if sub, ok := v.(*SubscriptionStatus); ok && sub != nil {
		return sub, nil
	}
	// Defensive fallback — every callback path today returns a non-nil
	// *SubscriptionStatus, but the comma-ok form guards against a
	// future return path that forgets to.
	return &SubscriptionStatus{BillingEnabled: false}, nil
}

// OpenCheckout opens Stripe hosted checkout in the default browser and waits
// for the payment to complete via a local callback server (same pattern as Login).
func (a *App) OpenCheckout(plan string) error {
	if plan != "byoa" && plan != "pro" && plan != "studio" && plan != "max" {
		return fmt.Errorf("invalid plan: %s", plan)
	}

	token, err := loadYansuToken()
	if err != nil || token == "" {
		return fmt.Errorf("not authenticated")
	}

	// Start local callback server on a random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to start callback server: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	successURL := fmt.Sprintf("http://localhost:%d/checkout-success", port)
	cancelURL := fmt.Sprintf("http://localhost:%d/checkout-cancel", port)

	// Create hosted checkout session via backend API
	apiBase := loadAPIBaseURL()
	req := map[string]string{
		"plan":        plan,
		"success_url": successURL,
		"cancel_url":  cancelURL,
	}
	var result CheckoutResult
	if err := apiPost(apiBase, token, "/api/users/me/subscription/checkout", req, &result); err != nil {
		return fmt.Errorf("failed to create checkout session: %w", err)
	}

	if result.CheckoutURL == "" {
		return fmt.Errorf("no checkout URL returned")
	}

	// Wait for callback with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	doneChan := make(chan bool, 1)
	errChan := make(chan error, 1)

	callbackStyles := brandThemeCSS()
	callbackPage := `<html data-theme="%s"><head><title>Isoform</title><style>%s</style></head><body style="font-family:system-ui;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:var(--color-background);color:var(--color-ink)"><div style="text-align:center"><h2>%s</h2><p style="color:var(--color-muted)">%s</p></div></body></html>`

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			switch r.URL.Path {
			case "/checkout-success":
				_, _ = w.Write([]byte(fmt.Sprintf(callbackPage, currentThemePreference(), callbackStyles,
					"Payment successful",
					"You can close this tab and return to Isoform.",
				)))
				doneChan <- true
			case "/checkout-cancel":
				_, _ = w.Write([]byte(fmt.Sprintf(callbackPage, currentThemePreference(), callbackStyles,
					"Payment cancelled",
					"You can close this tab and return to Isoform.",
				)))
				errChan <- fmt.Errorf("checkout cancelled by user")
			default:
				http.NotFound(w, r)
			}
		}),
	}

	go func() { _ = server.Serve(listener) }()
	defer server.Shutdown(context.Background())

	// Open checkout in default browser
	runtime.BrowserOpenURL(a.ctx, result.CheckoutURL)

	select {
	case <-doneChan:
		// Bring Wails window to front
		a.ShowWindow()
		// Wait for the Stripe webhook to activate the subscription before
		// returning, so the frontend refresh shows the updated plan. If the
		// webhook does not land in time, fall back to a one-shot Stripe sync
		// so the user is never stuck staring at a stale plan.
		a.waitForPlanActivation(token, apiBase, plan)
		// Drop the in-process cache before notifying — the next
		// GetSubscriptionStatus call (frontend refresh, pre-flight) must
		// see the post-checkout plan, not the stale cached pre-checkout
		// answer.
		invalidateSubscriptionStatusCache()
		// Notify frontend listeners (sidebar plan badge, paywall cards, etc.)
		// that subscription state has changed so cached data is invalidated.
		// ChangePlan emits this too; OpenCheckout used to skip it, leaving the
		// sidebar showing the pre-checkout plan until the next manual refresh.
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "subscription:changed")
		}
		return nil
	case err := <-errChan:
		a.ShowWindow()
		return err
	case <-ctx.Done():
		return fmt.Errorf("checkout timed out")
	}
}

// waitForPlanActivation polls the backend until subscription_plan reflects the
// plan the user just paid for. If the Stripe webhook is delayed or dropped and
// the plan never materializes within the timeout, fall back to forcing a
// reconcile against the live Stripe customer state — that way the user always
// returns to the GUI with the right plan, no manual recovery button needed.
func (a *App) waitForPlanActivation(token, apiBase, expectedPlan string) {
	for range 15 {
		time.Sleep(2 * time.Second)
		var status SubscriptionStatus
		if err := apiGet(apiBase, token, "/api/users/me/subscription", &status); err != nil {
			continue
		}
		if status.IsActive && status.Plan != nil && *status.Plan == expectedPlan {
			return
		}
	}
	// Webhook never updated the user record — pull state from Stripe directly.
	// Errors are swallowed: if sync also fails, the next normal poll will eventually
	// reconcile, and the worst case is a slightly stale UI for a few seconds.
	var synced SubscriptionStatus
	_ = apiPost(apiBase, token, "/api/users/me/subscription/sync", nil, &synced)
}

// CreateBillingPortalSession creates a Stripe billing portal session
func (a *App) CreateBillingPortalSession() (*PortalResult, error) {
	token, err := loadYansuToken()
	if err != nil || token == "" {
		return nil, fmt.Errorf("not authenticated")
	}

	apiBase := loadAPIBaseURL()
	var result PortalResult
	if err := apiPost(apiBase, token, "/api/users/me/subscription/portal", nil, &result); err != nil {
		return nil, fmt.Errorf("failed to create billing portal session: %w", err)
	}

	return &result, nil
}

// ChangePlan upgrades or downgrades the user's subscription plan
func (a *App) ChangePlan(plan string) (*SubscriptionStatus, error) {
	token, err := loadYansuToken()
	if err != nil || token == "" {
		return nil, fmt.Errorf("not authenticated")
	}

	if plan != "free" && plan != "pro" && plan != "studio" && plan != "max" {
		return nil, fmt.Errorf("invalid plan: %s", plan)
	}

	apiBase := loadAPIBaseURL()
	req := map[string]string{"plan": plan}
	var result SubscriptionStatus
	if err := apiPost(apiBase, token, "/api/users/me/subscription/change-plan", req, &result); err != nil {
		return nil, fmt.Errorf("failed to change plan: %w", err)
	}

	// Drop the in-process cache so subsequent reads see the new plan.
	invalidateSubscriptionStatusCache()
	// Notify any frontend listener that subscription state has changed.
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "subscription:changed")
	}

	return &result, nil
}

// QuotaUsageResult represents the user's current quota usage percentages
type QuotaUsageResult struct {
	Plan    string      `json:"plan"`
	Daily   QuotaPeriod `json:"daily"`
	Weekly  QuotaPeriod `json:"weekly"`
	Monthly QuotaPeriod `json:"monthly"`
}

// QuotaPeriod represents usage for a single quota period
type QuotaPeriod struct {
	Percentage int        `json:"percentage"`
	Limit      float64    `json:"limit"`
	ResetAt    *time.Time `json:"reset_at,omitempty"`
}

// GetQuotaUsage fetches the current user's quota usage percentages from the backend
func (a *App) GetQuotaUsage() (*QuotaUsageResult, error) {
	token, err := loadYansuToken()
	if err != nil || token == "" {
		return nil, fmt.Errorf("not authenticated")
	}

	apiBase := loadAPIBaseURL()
	var result QuotaUsageResult
	if err := apiGet(apiBase, token, "/api/users/me/subscription/usage", &result); err != nil {
		log.Printf("[subscription] failed to fetch quota usage: %v", err)
		return nil, err
	}

	return &result, nil
}

// CancelPendingDowngrade cancels a scheduled plan downgrade
func (a *App) CancelPendingDowngrade() error {
	token, err := loadYansuToken()
	if err != nil || token == "" {
		return fmt.Errorf("not authenticated")
	}

	apiBase := loadAPIBaseURL()
	if err := apiPost(apiBase, token, "/api/users/me/subscription/cancel-downgrade", nil, nil); err != nil {
		return fmt.Errorf("failed to cancel pending downgrade: %w", err)
	}

	// Drop the in-process cache + notify frontend listeners so the
	// pending-downgrade banner clears and any paywall gating refreshes
	// immediately — symmetric with ChangePlan / OpenCheckout.
	invalidateSubscriptionStatusCache()
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "subscription:changed")
	}

	return nil
}
