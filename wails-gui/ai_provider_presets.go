package main

import "strings"

// AIProviderPreset is a curated bring-your-own API provider entry. It
// prefills the endpoint and seed models so a user only pastes a key, and it
// is the single source of truth the model-listing fallback uses when a live
// /models fetch fails (so the dropdown is never left empty).
//
// This mirrors the "predefined providers" catalog pattern proven in other
// desktop agents: a small, extensible table that maps a friendly provider to
// (a) the wire format the runner speaks (APIFormat), (b) its default base
// URL, and (c) a seed model list. APIFormat is deliberately one of the two
// formats the in-process runner already understands ("openai" / "anthropic")
// — a preset is a selection convenience, not a new runner backend.
type AIProviderPreset struct {
	// ID is the stable catalog key, persisted as AIProviderConfig.PresetID so
	// the UI can restore the selection and the model listing can resolve the
	// seed fallback.
	ID string `json:"id"`
	// Name is the brand label shown in the picker (not translated).
	Name string `json:"name"`
	// APIFormat is the runner contract: "openai" or "anthropic". It becomes
	// AIProviderConfig.Provider on save.
	APIFormat string `json:"apiFormat"`
	// BaseURL is the prefilled endpoint. Empty means the official endpoint
	// for the format (api.openai.com / api.anthropic.com).
	BaseURL string `json:"baseUrl"`
	// SeedModels is the fallback shown when the live /models fetch fails or
	// returns nothing. Best-effort, not authoritative.
	SeedModels []string `json:"seedModels"`
	// APIKeyURL links to where the user obtains a key ("" hides the link).
	APIKeyURL string `json:"apiKeyUrl"`
	// RequiresKey is false for local providers (Ollama) that need no key.
	RequiresKey bool `json:"requiresKey"`
	// Custom marks the free-form entry where the user supplies everything;
	// it carries no seed list, so a failed fetch surfaces the error.
	Custom bool `json:"custom"`
}

// predefinedAIProviders is the curated BYOA provider catalog. Adding a
// provider is a one-entry change here. APIFormat values were verified against
// each vendor's wire protocol: DeepSeek / Moonshot / Qwen / Zhipu / OpenRouter
// / Ollama all expose an OpenAI-compatible /models + Bearer endpoint; only
// Anthropic uses the x-api-key + paginated /v1/models shape.
func predefinedAIProviders() []AIProviderPreset {
	return []AIProviderPreset{
		{
			ID:          "openai",
			Name:        "OpenAI",
			APIFormat:   "openai",
			BaseURL:     "",
			SeedModels:  []string{"gpt-4o", "gpt-4o-mini", "o3", "o3-mini"},
			APIKeyURL:   "https://platform.openai.com/api-keys",
			RequiresKey: true,
		},
		{
			ID:          "anthropic",
			Name:        "Anthropic",
			APIFormat:   "anthropic",
			BaseURL:     "",
			SeedModels:  []string{"claude-sonnet-5", "claude-haiku-4-5"},
			APIKeyURL:   "https://console.anthropic.com/settings/keys",
			RequiresKey: true,
		},
		{
			ID:          "deepseek",
			Name:        "DeepSeek",
			APIFormat:   "openai",
			BaseURL:     "https://api.deepseek.com",
			SeedModels:  []string{"deepseek-chat", "deepseek-reasoner"},
			APIKeyURL:   "https://platform.deepseek.com/api_keys",
			RequiresKey: true,
		},
		{
			ID:          "moonshot",
			Name:        "Moonshot (Kimi)",
			APIFormat:   "openai",
			BaseURL:     "https://api.moonshot.cn/v1",
			SeedModels:  []string{"moonshot-v1-8k", "moonshot-v1-32k", "moonshot-v1-128k", "kimi-k2-0711-preview"},
			APIKeyURL:   "https://platform.moonshot.cn/console/api-keys",
			RequiresKey: true,
		},
		{
			ID:          "qwen",
			Name:        "Qwen (DashScope)",
			APIFormat:   "openai",
			BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1",
			SeedModels:  []string{"qwen-plus", "qwen-max", "qwen-turbo"},
			APIKeyURL:   "https://bailian.console.aliyun.com/",
			RequiresKey: true,
		},
		{
			ID:          "zhipu",
			Name:        "Zhipu GLM",
			APIFormat:   "openai",
			BaseURL:     "https://open.bigmodel.cn/api/paas/v4",
			SeedModels:  []string{"glm-4-plus", "glm-4-air", "glm-4-flash"},
			APIKeyURL:   "https://bigmodel.cn/usercenter/apikeys",
			RequiresKey: true,
		},
		{
			ID:          "openrouter",
			Name:        "OpenRouter",
			APIFormat:   "openai",
			BaseURL:     "https://openrouter.ai/api/v1",
			SeedModels:  []string{"anthropic/claude-3.5-sonnet", "openai/gpt-4o", "google/gemini-pro-1.5"},
			APIKeyURL:   "https://openrouter.ai/keys",
			RequiresKey: true,
		},
		{
			ID:          "ollama",
			Name:        "Ollama (local)",
			APIFormat:   "openai",
			BaseURL:     "http://localhost:11434/v1",
			SeedModels:  nil,
			APIKeyURL:   "",
			RequiresKey: false,
		},
		{
			ID:          "custom",
			Name:        "Custom (OpenAI-compatible)",
			APIFormat:   "openai",
			BaseURL:     "",
			SeedModels:  nil,
			APIKeyURL:   "",
			RequiresKey: true,
			Custom:      true,
		},
	}
}

// aiProviderPresetByID resolves a catalog entry. Empty/unknown ids report
// false so callers fall back to format-only behaviour.
func aiProviderPresetByID(id string) (AIProviderPreset, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return AIProviderPreset{}, false
	}
	for _, p := range predefinedAIProviders() {
		if p.ID == id {
			return p, true
		}
	}
	return AIProviderPreset{}, false
}

// ListAIProviderPresets exposes the BYOA provider catalog to the desktop UI
// so the provider picker can prefill endpoints, seed models, and key links.
func (a *App) ListAIProviderPresets() []AIProviderPreset {
	return predefinedAIProviders()
}
