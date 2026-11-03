package main

import "strings"

// effectiveTaskModel resolves the role alias through the same persisted
// provider configuration used to build the runner. Durable provenance should
// identify the model that receives the request, not an internal role name.
func effectiveTaskModel(app *App, role string) string {
	if app == nil {
		return ""
	}
	role = strings.TrimSpace(role)
	if provider, ok := aiProviderActive(); ok {
		switch strings.ToLower(role) {
		case "haiku", "fable":
			if model := strings.TrimSpace(provider.lightModelOrChat()); model != "" {
				return model
			}
		default:
			if model := strings.TrimSpace(provider.Model); model != "" {
				return model
			}
		}
	}
	if external, ok := externalAgentActive(); ok {
		if model := strings.TrimSpace(external.ModelAliases[role]); model != "" {
			return model
		}
		// An external CLI with no pinned model resolves its own role aliases.
		// Preserve the alias rather than substituting an unrelated API model.
		return role
	}
	return claudeModelForAnthropicAPI(role)
}

func effectivePrimaryTaskModel(app *App) string {
	if app == nil {
		return ""
	}
	return effectiveTaskModel(app, app.chatTaskModel())
}

func effectiveLightTaskModel(app *App) string {
	if app == nil {
		return ""
	}
	return effectiveTaskModel(app, app.lightTaskModel())
}
