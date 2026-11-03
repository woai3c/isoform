package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Activity threads (layer 4): moments (composed activities) grouped into the
// threads of work a person follows through a day. The composer keeps moments
// small and precise; this layer puts the pieces back together. Rules here are
// pure so they are unit-testable: sure joins on concrete objects first, then
// one batched model judgment for the rest, then guards that keep the user's
// corrections and keep personal moments apart from work.

const (
	threadSourceRule = "rule"
	threadSourceAI   = "ai"
	threadSourceUser = "user"

	threadKindWork     = "work"
	threadKindPersonal = "personal"

	// threadPromptMomentLimit bounds one batched call. A busy day has about 80
	// moments; anything beyond the limit stays an ungrouped row.
	threadPromptMomentLimit = 150
	// threadPromptMemberTitles bounds how many moment titles describe an
	// existing thread in the prompt.
	threadPromptMemberTitles = 6
	threadPromptGoalLimit    = 12
	threadPromptFixLimit     = 8
	threadTitleMaxRunes      = 80
)

// threadMoment is the part of a moment the grouping reads. It is built only
// from moments that pass the model-privacy filter.
type threadMoment struct {
	ID          string
	StartMs     int64
	EndMs       int64
	Title       string
	Description string
	Category    string
	Entities    []Entity
	Apps        []string
	ContinuesID string
}

// threadState is a stored thread with its current members.
type threadState struct {
	ID        string
	Title     string
	UserTitle string
	Kind      string
	Members   []string
	// UserMade marks a thread the user split off; sure joins skip it.
	UserMade bool
}

// threadAssignment is the decision for one moment.
type threadAssignment struct {
	MomentID string
	ThreadID string // existing thread id, or a newThreadKey
	Source   string
	Reason   string
}

// threadNew is a thread the pass creates.
type threadNew struct {
	Key   string
	Title string
	Kind  string
}

// threadCorrectionExample is a remembered user correction, shown to the model.
type threadCorrectionExample struct {
	MomentTitle  string
	FromTitle    string
	ToTitle      string // empty when the moment was split into its own thread
	RenamedTitle string
}

// threadGoal is a current Purpose Map goal the model may name a thread after.
type threadGoal struct {
	Key   string
	Title string
}

// Tools are where work happens, not what the work is about. A project with the
// same name as the app the moment ran in is the tool too (a dictation in
// Isoform is not Isoform work by itself).
var threadToolNames = map[string]bool{
	"chatgpt": true, "claude": true, "claude code": true, "codex": true, "copilot": true,
	"gemini": true, "perplexity": true, "safari": true, "chrome": true, "google chrome": true,
	"finder": true, "terminal": true, "iterm2": true, "slack": true, "messages": true,
	"imessage": true, "mail": true, "gmail": true, "notes": true, "github": true,
	"isoform": true, "yansu": true, "wechat": true, "discord": true, "telegram": true,
	"facetime": true, "zoom": true, "google meet": true, "teams": true, "microsoft teams": true,
}

var threadToolDomains = map[string]bool{
	"chatgpt.com": true, "chat.openai.com": true, "openai.com": true, "claude.ai": true,
	"google.com": true, "github.com": true, "youtube.com": true, "mail.google.com": true,
	"docs.google.com": true, "localhost": true,
}

var threadDomainRE = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.)?([a-z0-9-]+(?:\.[a-z0-9-]+)*\.(?:ai|com|io|org|net|app|dev|co|so|xyz|me|gg))\b`)

func threadNorm(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

// threadSureKeys are the concrete objects that join moments without asking the
// model: a repo, pull request, named person (not the user), channel, site
// domain, or a project that is not the app the moment ran in.
func threadSureKeys(m threadMoment, selfNames map[string]bool) map[string]bool {
	apps := map[string]bool{}
	for _, app := range m.Apps {
		apps[threadNorm(app)] = true
	}
	keys := map[string]bool{}
	for _, entity := range m.Entities {
		name := threadNorm(entity.Name)
		if name == "" {
			continue
		}
		if match := threadDomainRE.FindStringSubmatch(name); match != nil {
			domain := strings.ToLower(match[1])
			if !threadToolDomains[domain] {
				keys["site:"+domain] = true
			}
		}
		switch entity.Type {
		case entityRepo, entityProject:
			head := name
			if i := strings.LastIndex(head, "/"); i >= 0 {
				head = head[i+1:]
			}
			// "isoform hub" is the isoform project.
			if fields := strings.Fields(head); len(fields) > 0 {
				head = fields[0]
			}
			if head == "" || selfNames[head] || (entity.Type == entityProject && apps[head]) {
				continue
			}
			keys["obj:"+head] = true
		case entityPR:
			keys["pr:"+name] = true
		case entityPerson:
			if selfNames[name] || threadToolNames[name] {
				continue
			}
			keys["person:"+name] = true
		case entityChannel:
			keys["channel:"+name] = true
		}
	}
	return keys
}

func threadSelfNames(displayName string) map[string]bool {
	out := map[string]bool{}
	name := threadNorm(displayName)
	if name == "" {
		return out
	}
	out[name] = true
	for _, field := range strings.Fields(name) {
		if utf8.RuneCountInString(field) >= 3 {
			out[field] = true
		}
	}
	return out
}

// threadRulesPass assigns each unassigned moment by sure joins. Gaps in time
// never split: a moment joins any thread of the day that shares an object.
// Two new moments that share an object start a thread together. It returns the
// assignments, the threads it started, and the moments left for the model.
func threadRulesPass(threads []threadState, threadKeys map[string]map[string]bool, assigned map[string]string, moments []threadMoment, selfNames map[string]bool) ([]threadAssignment, []threadState, []threadMoment) {
	keysOf := map[string]map[string]bool{}
	for id, keys := range threadKeys {
		threadAddKeys(keysOf, id, keys)
	}
	closed := map[string]bool{} // personal or user-made: no sure joins
	order := make([]string, 0, len(threads))
	for _, thread := range threads {
		order = append(order, thread.ID)
		if thread.Kind == threadKindPersonal || thread.UserMade {
			closed[thread.ID] = true
		}
	}
	var out []threadAssignment
	var started []threadState
	var pending []threadMoment
	pendingKeys := map[string]map[string]bool{}
	sorted := append([]threadMoment(nil), moments...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartMs < sorted[j].StartMs })
	for _, moment := range sorted {
		if _, ok := assigned[moment.ID]; ok {
			continue
		}
		keys := threadSureKeys(moment, selfNames)
		// A composer continuation stays in the thread it continues.
		if target, ok := assigned[moment.ContinuesID]; moment.ContinuesID != "" && ok && !closed[target] {
			out = append(out, threadAssignment{MomentID: moment.ID, ThreadID: target, Source: threadSourceRule, Reason: "continues"})
			assigned[moment.ID] = target
			threadAddKeys(keysOf, target, keys)
			continue
		}
		best, bestShared := "", 0
		for _, id := range order {
			if closed[id] {
				continue
			}
			if shared := threadSharedCount(keys, keysOf[id]); shared > bestShared {
				best, bestShared = id, shared
			}
		}
		if best != "" {
			out = append(out, threadAssignment{MomentID: moment.ID, ThreadID: best, Source: threadSourceRule, Reason: threadSharedReason(keys, keysOf[best])})
			assigned[moment.ID] = best
			threadAddKeys(keysOf, best, keys)
			continue
		}
		seed := -1
		for i, earlier := range pending {
			if threadSharedCount(keys, pendingKeys[earlier.ID]) > 0 {
				seed = i
				break
			}
		}
		if seed < 0 {
			pending = append(pending, moment)
			pendingKeys[moment.ID] = keys
			continue
		}
		first := pending[seed]
		pending = append(pending[:seed], pending[seed+1:]...)
		id := threadIDForMoment(first.ID)
		started = append(started, threadState{ID: id, Title: first.Title, Kind: threadKindWork, Members: []string{first.ID, moment.ID}})
		order = append(order, id)
		threadAddKeys(keysOf, id, pendingKeys[first.ID])
		reason := threadSharedReason(keys, keysOf[id])
		threadAddKeys(keysOf, id, keys)
		out = append(out,
			threadAssignment{MomentID: first.ID, ThreadID: id, Source: threadSourceRule, Reason: reason},
			threadAssignment{MomentID: moment.ID, ThreadID: id, Source: threadSourceRule, Reason: reason})
		assigned[first.ID], assigned[moment.ID] = id, id
	}
	return out, started, pending
}

func threadIDForMoment(momentID string) string { return "th_" + momentID }

// threadSeedKey names a thread the model proposes before it has an id.
func threadSeedKey(momentID string) string { return "new:" + momentID }

func threadSharedCount(a, b map[string]bool) int {
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	return n
}

func threadAddKeys(keysOf map[string]map[string]bool, id string, keys map[string]bool) {
	if keysOf[id] == nil {
		keysOf[id] = map[string]bool{}
	}
	for k := range keys {
		keysOf[id][k] = true
	}
}

func threadSharedReason(keys, threadKeys map[string]bool) string {
	var shared []string
	for k := range keys {
		if threadKeys[k] {
			shared = append(shared, k)
		}
	}
	sort.Strings(shared)
	if len(shared) == 0 {
		return ""
	}
	return shared[0]
}

// ── Model judgment ──

type threadPromptInput struct {
	ReasonSources string
	ReasonAux     string
	ReasonContext string            // fingerprint for the stored model explanation, never provider input
	ReasonTitles  map[string]string // hashes of the exact existing titles supplied to the provider
	Threads       []threadState
	ThreadTitle   map[string]string // member id -> title for describing threads
	Moments       []threadMoment    // moments the model decides
	RuleJoined    map[string]string // moment id -> thread id chosen by rules this pass
	Goals         []threadGoal
	Corrections   []threadCorrectionExample
	Language      string
}

type threadModelMoment struct {
	ID         string `json:"id"`
	Thread     string `json:"thread"`
	Kind       string `json:"kind"`
	Confidence string `json:"confidence"`
	Reason     string `json:"reason"`
}

type threadModelThread struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	Goal  string `json:"goal"`
}

type threadModelResponse struct {
	Moments []threadModelMoment `json:"moments"`
	Threads []threadModelThread `json:"threads"`
	// Merge lists pairs of existing threads that are one line of work; the
	// second folds into the first.
	Merge [][]string `json:"merge"`
}

// threadPromptAliases maps short prompt ids to real ids, so the prompt never
// carries storage identifiers.
type threadPromptAliases struct {
	threadByAlias map[string]string
	aliasByThread map[string]string
	momentByAlias map[string]string
	aliasByMoment map[string]string
}

func buildThreadPrompt(in threadPromptInput) (string, threadPromptAliases) {
	aliases := threadPromptAliases{
		threadByAlias: map[string]string{}, aliasByThread: map[string]string{},
		momentByAlias: map[string]string{}, aliasByMoment: map[string]string{},
	}
	var b strings.Builder
	b.WriteString(`You group a person's day into threads of work. A moment is one small piece of activity; a thread is one line of work or one personal matter the person follows through the day, often with interruptions.

Decide, for each new moment, which thread it belongs to.
- Join a thread when the moment serves the same project or goal, the same people, the same document, repo or site, or the same topic. A moment that returns to a thread after an interruption joins it.
- Research done for a project (for example studying a competitor's feature) belongs to that project's thread.
- Personal matters (family, relationships, health, personal reflections, private errands) are kind "personal". A personal moment never joins a work thread, and a work moment never joins a personal thread, even when both were typed or dictated in the same app.
- The app a moment ran in is not a reason to join. Chat titles listed in an app's sidebar are not the moment's subject.
- Short maintenance moments (updating an app, fixing a sign-in) may share one thread named for quick fixes.
- When unsure, say "new" with confidence "low". A wrong join is worse than a separate thread.
- A moment marked "rules joined" shares a concrete object with that thread. Keep it there unless it is personal, or the shared object is clearly incidental (seen on screen but not what the moment is about); then move it with confidence "high".
- When two threads so far are one line of work, list them in "merge" as ["t1","t2"]; the second folds into the first.

Then give every thread that gained moments (and every new thread) a short title: the project or subject first, then the two or three things done, like "Isoform: brief fix, Activity review, workers". No times. When a thread serves one of the person's goals, set "goal" to that goal's id.
`)
	if in.Language != "" {
		b.WriteString(in.Language)
		b.WriteString("\n")
	}
	var groupingFixes, namingFixes []threadCorrectionExample
	for _, fix := range in.Corrections {
		if fix.RenamedTitle != "" {
			namingFixes = append(namingFixes, fix)
		} else {
			groupingFixes = append(groupingFixes, fix)
		}
	}
	if len(groupingFixes) > 0 {
		b.WriteString("\nThe person corrected earlier grouping. Follow the same judgment:\n")
		for _, fix := range groupingFixes {
			if fix.ToTitle == "" {
				fmt.Fprintf(&b, "- moved %q out of %q into its own thread\n", threadPromptText(fix.MomentTitle, 120), threadPromptText(fix.FromTitle, 80))
			} else {
				fmt.Fprintf(&b, "- moved %q from %q to %q\n", threadPromptText(fix.MomentTitle, 120), threadPromptText(fix.FromTitle, 80), threadPromptText(fix.ToTitle, 80))
			}
		}
	}
	if len(in.Goals) > 0 {
		b.WriteString("\nThe person's current goals:\n")
		for i, goal := range in.Goals {
			if i >= threadPromptGoalLimit {
				break
			}
			fmt.Fprintf(&b, "- g%d: %s\n", i+1, threadPromptText(goal.Title, 120))
		}
	}
	b.WriteString("\nThreads so far today:\n")
	if len(in.Threads) == 0 {
		b.WriteString("(none)\n")
	}
	for i, thread := range in.Threads {
		alias := fmt.Sprintf("t%d", i+1)
		aliases.threadByAlias[alias] = thread.ID
		aliases.aliasByThread[thread.ID] = alias
		title := thread.UserTitle
		if title == "" {
			title = thread.Title
		}
		kind := thread.Kind
		if kind == "" {
			kind = threadKindWork
		}
		fmt.Fprintf(&b, "- %s (%s): %s\n", alias, kind, threadPromptText(title, 120))
		shown := 0
		for _, member := range thread.Members {
			memberTitle := in.ThreadTitle[member]
			if memberTitle == "" {
				continue
			}
			fmt.Fprintf(&b, "  - %s\n", threadPromptText(memberTitle, 120))
			shown++
			if shown >= threadPromptMemberTitles {
				break
			}
		}
	}
	b.WriteString("\nNew moments:\n")
	for i, moment := range in.Moments {
		if i >= threadPromptMomentLimit {
			break
		}
		alias := fmt.Sprintf("m%d", i+1)
		aliases.momentByAlias[alias] = moment.ID
		aliases.aliasByMoment[moment.ID] = alias
		fmt.Fprintf(&b, "- %s %s: %s", alias, time.UnixMilli(moment.StartMs).Format("15:04"), threadPromptText(moment.Title, 160))
		if moment.Description != "" {
			fmt.Fprintf(&b, " | %s", threadPromptText(moment.Description, 200))
		}
		if objects := threadPromptEntities(moment.Entities); objects != "" {
			fmt.Fprintf(&b, " | about: %s", objects)
		}
		if len(moment.Apps) > 0 {
			fmt.Fprintf(&b, " | apps: %s", strings.Join(moment.Apps, ", "))
		}
		if moment.Category != "" {
			fmt.Fprintf(&b, " | %s", moment.Category)
		}
		if target := in.RuleJoined[moment.ID]; target != "" {
			if alias := aliases.aliasByThread[target]; alias != "" {
				fmt.Fprintf(&b, " | rules joined %s", alias)
			}
		}
		b.WriteString("\n")
	}
	if len(namingFixes) > 0 {
		b.WriteString("\nNaming examples only. Use these to choose title wording. Ignore them when deciding membership, kind or merges:\n")
		for _, fix := range namingFixes {
			fmt.Fprintf(&b, "- renamed a thread to %q\n", threadPromptText(fix.RenamedTitle, 80))
		}
	}
	b.WriteString(`
Reply with JSON only:
{"moments":[{"id":"m1","thread":"t1 or new1","kind":"work or personal","confidence":"high, medium or low","reason":"a few words"}],
 "threads":[{"id":"t1 or new1","title":"...","kind":"work or personal","goal":"g1 or empty"}],
 "merge":[]}
Use new1, new2 and so on for new threads; several moments may share one new thread.`)
	return b.String(), aliases
}

func threadPromptText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "..."
}

func threadPromptEntities(entities []Entity) string {
	var parts []string
	for _, entity := range entities {
		name := strings.TrimSpace(entity.Name)
		if name == "" || threadToolNames[threadNorm(name)] {
			continue
		}
		if u, err := url.Parse(name); err == nil && u.Host != "" {
			name = u.Host
		}
		parts = append(parts, threadPromptText(name, 60))
		if len(parts) >= 8 {
			break
		}
	}
	return strings.Join(parts, "; ")
}

// threadApplyModel turns a model response into assignments under the guards:
// unknown or low-confidence answers start their own thread, rule joins stay
// unless the moment is personal, personal and work never share a thread, and
// a moment the model left out starts its own thread so it is not re-asked.
func threadApplyModel(in threadPromptInput, aliases threadPromptAliases, resp threadModelResponse) ([]threadAssignment, []threadNew, map[string]string) {
	deciding := map[string]bool{}
	for _, moment := range in.Moments {
		deciding[moment.ID] = true
	}
	kindOf := map[string]string{}
	for _, thread := range in.Threads {
		kind := thread.Kind
		if kind == "" {
			kind = threadKindWork
		}
		kindOf[thread.ID] = kind
	}
	// A thread made only of moments decided in this call (one the rules just
	// started) takes the kind the model gives it.
	for _, answer := range resp.Threads {
		id, ok := aliases.threadByAlias[strings.TrimSpace(answer.ID)]
		if !ok {
			continue
		}
		fresh := true
		for _, thread := range in.Threads {
			if thread.ID != id {
				continue
			}
			for _, member := range thread.Members {
				if !deciding[member] {
					fresh = false
				}
			}
			if thread.UserMade {
				fresh = false
			}
		}
		if fresh {
			kindOf[id] = threadNormKind(answer.Kind)
		}
	}
	newKinds := map[string]string{}
	newTitles := map[string]string{}
	existingTitles := map[string]string{}
	for _, thread := range resp.Threads {
		alias := strings.TrimSpace(thread.ID)
		title := threadCleanTitle(thread.Title)
		kind := threadNormKind(thread.Kind)
		if id, ok := aliases.threadByAlias[alias]; ok {
			if title != "" {
				existingTitles[id] = title
			}
			continue
		}
		if strings.HasPrefix(alias, "new") {
			newKinds[alias] = kind
			newTitles[alias] = title
		}
	}
	decided := map[string]threadModelMoment{}
	for _, moment := range resp.Moments {
		if id, ok := aliases.momentByAlias[strings.TrimSpace(moment.ID)]; ok {
			decided[id] = moment
		}
	}
	var out []threadAssignment
	news := map[string]*threadNew{}
	var newOrder []string
	ensureNew := func(key, title, kind string) {
		if _, ok := news[key]; ok {
			return
		}
		news[key] = &threadNew{Key: key, Title: title, Kind: kind}
		newOrder = append(newOrder, key)
	}
	for i, moment := range in.Moments {
		if i >= threadPromptMomentLimit {
			break
		}
		own := threadSeedKey(moment.ID)
		answer, ok := decided[moment.ID]
		ruleTarget := in.RuleJoined[moment.ID]
		kind := threadNormKind(answer.Kind)
		reason := threadPromptText(redactThreadOutcomeText(answer.Reason), 120)
		switch {
		case !ok:
			if ruleTarget != "" {
				continue // the rules decision stands
			}
			ensureNew(own, moment.Title, threadKindWork)
			out = append(out, threadAssignment{MomentID: moment.ID, ThreadID: own, Source: threadSourceAI, Reason: "not answered"})
			continue
		case ruleTarget != "" && kind != threadKindPersonal && !threadModelOverrulesRule(answer, ruleTarget, aliases, kindOf, newKinds):
			continue // a concrete shared object outranks an unsure model
		}
		target := strings.TrimSpace(answer.Thread)
		lowConfidence := strings.EqualFold(strings.TrimSpace(answer.Confidence), "low")
		if id, exists := aliases.threadByAlias[target]; exists && !lowConfidence {
			if kindOf[id] == kind {
				out = append(out, threadAssignment{MomentID: moment.ID, ThreadID: id, Source: threadSourceAI, Reason: reason})
				continue
			}
		}
		if strings.HasPrefix(target, "new") && !lowConfidence {
			newKind := newKinds[target]
			if newKind == "" {
				newKind = kind
			}
			if newKind == kind {
				ensureNew(target, newTitles[target], kind)
				out = append(out, threadAssignment{MomentID: moment.ID, ThreadID: target, Source: threadSourceAI, Reason: reason})
				continue
			}
		}
		ensureNew(own, moment.Title, kind)
		out = append(out, threadAssignment{MomentID: moment.ID, ThreadID: own, Source: threadSourceAI, Reason: reason})
	}
	// Merges fold one existing work thread into another of the same kind.
	// Moments the user placed never move: the store keeps user rows.
	userMade := map[string]bool{}
	members := map[string][]string{}
	for _, thread := range in.Threads {
		userMade[thread.ID] = thread.UserMade
		members[thread.ID] = thread.Members
	}
	decidedHere := map[string]bool{}
	for _, as := range out {
		decidedHere[as.MomentID] = true
	}
	for _, pair := range resp.Merge {
		if len(pair) != 2 {
			continue
		}
		survivor, okA := aliases.threadByAlias[strings.TrimSpace(pair[0])]
		folded, okB := aliases.threadByAlias[strings.TrimSpace(pair[1])]
		if !okA || !okB || survivor == folded || kindOf[survivor] != kindOf[folded] || userMade[survivor] || userMade[folded] {
			continue
		}
		for _, id := range members[folded] {
			if !decidedHere[id] {
				out = append(out, threadAssignment{MomentID: id, ThreadID: survivor, Source: threadSourceAI, Reason: "merged"})
				decidedHere[id] = true
			}
		}
		for i := range out {
			if out[i].ThreadID == folded {
				out[i].ThreadID = survivor
			}
		}
		members[survivor] = append(members[survivor], members[folded]...)
		members[folded] = nil
	}
	created := make([]threadNew, 0, len(newOrder))
	for _, key := range newOrder {
		created = append(created, *news[key])
	}
	return out, created, existingTitles
}

// threadModelOverrulesRule reports whether a confident answer moves a rule
// join elsewhere: a different thread of the same kind, said with high
// confidence. Shared objects seen only in passing (a project name visible on
// screen) are what this catches.
func threadModelOverrulesRule(answer threadModelMoment, ruleTarget string, aliases threadPromptAliases, kindOf, newKinds map[string]string) bool {
	if !strings.EqualFold(strings.TrimSpace(answer.Confidence), "high") {
		return false
	}
	target := strings.TrimSpace(answer.Thread)
	kind := threadNormKind(answer.Kind)
	if id, ok := aliases.threadByAlias[target]; ok {
		return id != ruleTarget && kindOf[id] == kind
	}
	if strings.HasPrefix(target, "new") {
		newKind := newKinds[target]
		return newKind == "" || newKind == kind
	}
	return false
}

func threadNormKind(kind string) string {
	if strings.EqualFold(strings.TrimSpace(kind), threadKindPersonal) {
		return threadKindPersonal
	}
	return threadKindWork
}

func threadCleanTitle(title string) string {
	title = strings.Trim(strings.Join(strings.Fields(title), " "), `"'`)
	return threadPromptText(redactThreadOutcomeText(title), threadTitleMaxRunes)
}

func parseThreadModelResponse(raw string) (threadModelResponse, error) {
	var resp threadModelResponse
	raw = stripCodeFences(strings.TrimSpace(raw))
	if err := unmarshalJSONBestEffort(raw, &resp); err != nil {
		return threadModelResponse{}, fmt.Errorf("parse thread response: %w", err)
	}
	return resp, nil
}

func threadEntitiesFromJSON(raw string) []Entity {
	var entities []Entity
	if err := json.Unmarshal([]byte(raw), &entities); err != nil {
		return nil
	}
	return entities
}
