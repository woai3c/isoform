package main

import (
	"path"
	"regexp"
	"strings"
	"unicode"
)

var (
	topicNameExtension = regexp.MustCompile(`\.[a-zA-Z][a-zA-Z0-9]*\b`)
	topicNameRelease   = regexp.MustCompile(`(?i)\brc\s*([0-9]+)\b`)
	topicNameMainChat  = regexp.MustCompile(`(?i)^(?:(?:isoform|yansu)[ :·—-]*)?(?:main chat|chat(?: page)?|主聊天|聊天(?:页面)?)(?:\s*[-—|·]\s*(?:isoform|yansu))?$`)
	topicNameIsoform   = regexp.MustCompile(`(?i)\biso\s+form\b`)
)

// Display names never supply identity. MemberKeys retain the original repo,
// path and slug for matching; Aliases remain redirects between topic ids.
// This idempotent migration changes only Name, including on the next build of
// a registry saved before human names were introduced.
func migrateTopicDisplayName(project topicProject) topicProject {
	project.Name = topicHumanName(project.Name)
	return project
}

func topicHumanName(raw string) string {
	name := topicText(raw)
	if !topicDisplayNameSafe(name) {
		return ""
	}
	// Human evidence owns its spelling, including dotted product names and
	// leading lowercase brands. Only positively identified machine labels
	// receive filename cleanup and capitalization.
	if !topicMachineName(name) {
		return topicSafeHumanName(topicNameIsoform.ReplaceAllString(name, "Isoform"))
	}
	name = strings.ReplaceAll(name, `\`, "/")
	name = strings.TrimPrefix(name, "https://github.com/")
	name = strings.TrimPrefix(name, "http://github.com/")
	// Local paths and file references name their subject, never their folders.
	if strings.HasPrefix(name, "~") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "./") || strings.Count(name, "/") > 1 || strings.Contains(name, "/") && topicNameExtension.MatchString(name) {
		name = path.Base(name)
	} else if parts := strings.Split(name, "/"); len(parts) == 2 {
		owner, repo := parts[0], parts[1]
		// Organizations often append "ai" to a brand that the repo repeats.
		brand := strings.TrimSuffix(owner, "ai")
		if strings.EqualFold(owner, repo) {
			name = repo
		} else if len(repo) >= 4 && strings.HasPrefix(strings.ToLower(brand), strings.ToLower(repo)) {
			name = brand
		} else {
			name = owner + " " + repo
		}
	}
	name = topicNameExtension.ReplaceAllString(name, "")
	name = strings.NewReplacer("/", " ", "~", "", "_", " ", "-", " ").Replace(name)
	name = topicNameRelease.ReplaceAllString(name, "RC $1")
	// Isoform is the app's own brand, including when speech splits it.
	name = topicNameIsoform.ReplaceAllString(name, "Isoform")
	name = strings.Join(strings.Fields(name), " ")
	runes := []rune(name)
	if len(runes) > 0 {
		runes[0] = unicode.ToUpper(runes[0])
	}
	return topicSafeHumanName(string(runes))
}

// A masked subject is withheld rather than named from its remaining fragments.
// Check both before filename cleanup and after redaction/truncation. Main chat
// is the container for topics, and never a subject of its own.
func topicDisplayNameSafe(name string) bool {
	return strings.TrimSpace(name) != "" &&
		!strings.Contains(strings.ToUpper(name), "[REDACTED") &&
		!mobileBriefPrivacyMask.MatchString(strings.ToLower(name)) &&
		!topicNameMainChat.MatchString(strings.TrimSpace(topicNameIsoform.ReplaceAllString(name, "Isoform")))
}

func topicSafeHumanName(name string) string {
	name = topicText(name)
	if !topicDisplayNameSafe(name) {
		return ""
	}
	return name
}

func topicMachineName(name string) bool {
	if strings.ContainsAny(name, "/~\\_") || strings.Contains(name, "-") && len(strings.Fields(name)) == 1 {
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".txt", ".pdf", ".json", ".jsonl", ".csv", ".tsv", ".yaml", ".yml", ".go", ".tsx", ".jsx", ".html", ".xml":
		return true
	default:
		return false
	}
}

// Dotted product names are human evidence too. A code extension alone, or
// being a repo entity, is not file evidence. Require a path, an observed file
// path with this basename, or a conventional source filename such as main.js.
func topicActivityFileIdentifier(activity topicActivityObservation, name string) bool {
	normalized := strings.ReplaceAll(name, `\`, "/")
	ext := path.Ext(normalized)
	switch strings.ToLower(ext) {
	case ".ts", ".js", ".py", ".css", ".swift":
	default:
		return false
	}
	if strings.Contains(normalized, "/") {
		return true
	}
	switch strings.ToLower(strings.TrimSuffix(path.Base(normalized), ext)) {
	case "main", "index", "app", "style", "styles":
		return true
	}
	for _, token := range activity.Row.EvidenceTokens {
		if token.Kind == "path" && strings.EqualFold(path.Base(strings.ReplaceAll(token.Value, `\`, "/")), normalized) {
			return true
		}
	}
	return false
}

// Use only the activity evidence already admitted by the registry. In
// particular, naming never opens an arbitrary repo README or chat transcript
// outside that source's privacy checks. Project entities preserve the brand
// as written in window/README/chat observations, ahead of repo identifiers.
func topicActivityDisplayName(activity topicActivityObservation, raw string) string {
	moment := threadMomentFromRow(activity.Row)
	for _, entity := range moment.Entities {
		if entity.Type == entityProject && strings.TrimSpace(entity.Name) != "" && !topicMachineName(entity.Name) && !topicActivityFileIdentifier(activity, entity.Name) {
			for _, admitted := range activity.KeyNames {
				if strings.EqualFold(admitted, strings.TrimSpace(entity.Name)) {
					return topicHumanName(entity.Name)
				}
			}
		}
	}
	name := topicHumanName(raw)
	title := strings.TrimSpace(activity.Row.Title)
	for _, separator := range []string{" - ", " — ", ": "} {
		if before, _, ok := strings.Cut(title, separator); ok {
			title = before
		}
	}
	title = topicHumanName(title)
	if topicActivityFileIdentifier(activity, raw) {
		if title != "" && !topicMachineName(title) && !topicActivityFileIdentifier(activity, title) {
			return title
		}
		return topicHumanName(strings.TrimSuffix(raw, path.Ext(raw)))
	}
	// Prefer observed spelling and capitalization when a title names this
	// subject, without turning a whole activity description into a topic name.
	leaf := topicHumanName(path.Base(strings.ReplaceAll(raw, `\`, "/")))
	if topicNamesEqual(title, name) || topicNamesEqual(title, leaf) || strings.HasPrefix(topicNameKey(name), topicNameKey(title)+" ") && title != "" {
		return title
	}
	// An opaque checkout or file name is not a human subject. If the
	// admitted title names the work instead, use that dominant activity.
	if topicMachineName(raw) {
		if len(strings.Fields(title)) > 1 && !topicMachineName(activity.Row.Title) {
			return title
		}
	}
	switch strings.ToLower(name) {
	case "readme", "main", "index", "src", "docs", "project":
		if title != "" && !topicNamesEqual(title, name) {
			return title
		}
	}
	return name
}
