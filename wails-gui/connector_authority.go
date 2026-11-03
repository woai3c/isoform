package main

import (
	"encoding/json"
	"path/filepath"
	"strings"

	agent "yansu-agent/agent"
	"yansu-agent/agent/authority"
)

// Inbound connector runs (Telegram, Slack, Discord, Feishu, WeChat and the
// relayed chat apps) answer a message from someone other than the user, and
// nobody reviews the run. They run unattended, so every tool is denied unless
// a grant covers it. These grants give them read-only reach: Read, Glob and
// Grep inside the run's own scratch folder, the opencli searches the app's
// research schedules use, and WebSearch. Shell commands, writes, web fetches
// (a fetched URL can carry data out in its query string) and every other
// external call stay denied, and the protected-path check still applies.

// connectorRunGrants returns the authority grants of an inbound connector run
// whose scratch folder is workDir.
func connectorRunGrants(workDir string) []authority.Grant {
	grants := []authority.Grant{{ID: "connector-run:websearch", Capability: "tool.websearch"}}
	if dir := strings.TrimSpace(workDir); dir != "" && filepath.IsAbs(dir) {
		canonical := resolvedFolder(dir)
		for _, capability := range []string{"tool.read", "tool.glob", "tool.grep"} {
			grants = append(grants, authority.Grant{
				ID:             "connector-run:" + strings.TrimPrefix(capability, "tool."),
				Capability:     capability,
				ResourcePrefix: canonical,
			})
		}
	}
	grants = append(grants, authority.Grant{ID: "connector-run:opencli-list", Capability: "connector.command.execute", ResourcePrefix: "opencli:list/"})
	for _, adapter := range appResearchAdapters {
		for _, sub := range []string{"ask", "help"} {
			grants = append(grants, authority.Grant{
				ID:             "connector-run:opencli-" + adapter + "-" + sub,
				Capability:     "connector.command.execute",
				ResourcePrefix: "opencli:" + adapter + ":" + sub + "/",
			})
		}
	}
	return grants
}

// connectorRefusals collects what a connector run was refused, in plain
// words, so the reply can say it instead of a model-written policy message.
type connectorRefusals struct {
	seen  map[string]bool
	order []string
}

// observe records a refusal from an authority decision or a blocked tool
// result.
func (c *connectorRefusals) observe(event *agent.StreamEvent) {
	if event == nil {
		return
	}
	switch event.Type {
	case "authority_decision":
		var evaluation authority.Evaluation
		if json.Unmarshal([]byte(event.Content), &evaluation) != nil {
			return
		}
		if evaluation.Decision == authority.DecisionAllow {
			return
		}
		c.add(authority.Describe(evaluation.Action))
	case "tool_result":
		if strings.Contains(event.Content, "Isoform's own data folder") {
			c.add("read Isoform's own data folder")
		} else if strings.Contains(event.Content, "matches the denied command prefix") {
			c.add("push or publish on your behalf")
		}
	default:
	}
}

func (c *connectorRefusals) add(what string) {
	if what == "" {
		return
	}
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if c.seen[what] {
		return
	}
	c.seen[what] = true
	c.order = append(c.order, what)
}

// note returns the line that tells the sender what the run could not do, or
// "" when nothing was refused.
func (c *connectorRefusals) note(platform string) string {
	if len(c.order) == 0 {
		return ""
	}
	what := c.order[0]
	if n := len(c.order); n > 1 {
		what = strings.Join(c.order[:n-1], ", ") + " or " + c.order[n-1]
	}
	return "Isoform could not " + what + " for this message. Messages from " + platform +
		" can only read files in this chat's own folder and search the web; open Isoform on your computer to do more."
}

// withRefusalNote appends the refusal note to a connector reply.
func withRefusalNote(reply string, refusals *connectorRefusals, platform string) string {
	note := refusals.note(platform)
	if note == "" {
		return reply
	}
	if strings.TrimSpace(reply) == "" {
		return note
	}
	return strings.TrimRight(reply, "\n") + "\n\n" + note
}

// connectorDisplayName is a chat app's name as the user knows it.
func connectorDisplayName(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "slack":
		return "Slack"
	case "feishu":
		return "Feishu"
	case "lark":
		return "Lark"
	case "telegram":
		return "Telegram"
	case "discord":
		return "Discord"
	case "wechat":
		return "WeChat"
	case "whatsapp":
		return "WhatsApp"
	default:
	}
	return "this chat app"
}
