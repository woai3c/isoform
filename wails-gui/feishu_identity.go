package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

const feishuIdentityApp = "feishu"

// feishuBotNameInflight dedupes concurrent lazy bot-name backfills per brand so
// the bounded GetFeishuStatus polling can't spawn a goroutine storm.
var feishuBotNameInflight sync.Map // brand -> struct{}

// refreshStoredFeishuBotName lazily fetches the connected bot application's
// display name (e.g. "<registrant>的智能助手") and persists it on the brand's
// credentials so the connection card can identify the bot instead of the human
// registrant. No-op once BotName is already stored.
func (a *App) refreshStoredFeishuBotName(brand string) {
	brand = normalizeFeishuBrand(brand)
	if _, busy := feishuBotNameInflight.LoadOrStore(brand, struct{}{}); busy {
		return
	}
	defer feishuBotNameInflight.Delete(brand)

	// Reload fresh so we only set BotName and never clobber a username that a
	// concurrent identity refresh may have just persisted.
	creds := loadFeishuLocalCredentials(brand)
	if creds == nil || strings.TrimSpace(creds.BotName) != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	name, err := newFeishuLocalPoster(*creds).getBotAppName(ctx)
	if err != nil {
		log.Printf("[feishu-local] bot app name lookup unavailable: %v", err)
		return
	}
	if name = strings.TrimSpace(name); name == "" {
		return
	}
	creds.BotName = name
	if err := saveFeishuLocalCredentials(creds); err != nil {
		log.Printf("[feishu-local] failed to persist bot app name: %v", err)
	}
}

func (a *App) enrichFeishuIdentity(ctx context.Context, creds *feishuLocalCredentials) {
	if creds == nil || strings.TrimSpace(creds.OpenID) == "" || strings.TrimSpace(creds.UserName) != "" {
		return
	}
	poster := newFeishuLocalPoster(*creds)
	name, err := poster.getUserDisplayName(ctx, creds.OpenID)
	if err != nil {
		log.Printf("[feishu-local] current user lookup unavailable: %v", err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		// contact/v3/users frequently returns an empty name for scan-created
		// PersonalAgent apps because the app's contact data scope excludes the
		// registrant, even with full permissions granted. Fall back to the
		// auto-generated bot app name ("<name>的智能助手"), which carries the
		// registrant's display name and needs no contact permission.
		appName, appErr := poster.getBotAppName(ctx)
		if appErr != nil {
			log.Printf("[feishu-local] bot app name lookup unavailable: %v", appErr)
		}
		name = personalAgentDisplayName(appName)
	}
	creds.UserName = name
}

// personalAgentDisplayName extracts the registrant's display name from a
// scan-created PersonalAgent bot app name. Feishu (mainland) names these
// "<name>的智能助手"; Lark (international) uses an English possessive form.
// Returns "" when no known template matches so callers keep the existing
// account-ID fallback instead of surfacing a bot-shaped label.
func personalAgentDisplayName(appName string) string {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return ""
	}
	for _, suffix := range []string{
		"的智能助手",
		"'s AI Assistant", "’s AI Assistant",
		"'s Assistant", "’s Assistant",
	} {
		if trimmed, found := strings.CutSuffix(appName, suffix); found {
			if trimmed = strings.TrimSpace(trimmed); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func (a *App) bindFeishuSelfIdentity(ctx context.Context, creds *feishuLocalCredentials) error {
	if creds == nil || strings.TrimSpace(creds.OpenID) == "" {
		return nil
	}
	// Feishu and Lark are separate account systems; bind each under its own app
	// key so the People self gets a distinct handle per brand.
	return a.bindIntegrationSelfIdentity(ctx, normalizeFeishuBrand(creds.Brand), creds.OpenID, creds.UserName)
}

func (a *App) refreshStoredFeishuIdentity(creds feishuLocalCredentials) {
	if strings.TrimSpace(creds.UserName) != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	a.enrichFeishuIdentity(ctx, &creds)
	if strings.TrimSpace(creds.UserName) == "" {
		return
	}
	if err := saveFeishuLocalCredentials(&creds); err != nil {
		log.Printf("[feishu-local] failed to persist current user name: %v", err)
		return
	}
	if err := updateConfig(func(c *yansuConfig) {
		if cfg := feishuBrandConfig(c, creds.Brand); cfg != nil && cfg.AppID == creds.AppID {
			cfg.FeishuUserName = creds.UserName
		}
	}); err != nil {
		log.Printf("[feishu-local] failed to update current user name in config: %v", err)
	}
	if err := a.bindFeishuSelfIdentity(ctx, &creds); err != nil {
		log.Printf("[feishu-local] failed to refresh People self binding: %v", err)
	}
}
