package appparser

import (
	"strings"
)

// remindersBundleIDs lists the macOS Reminders app.
var remindersBundleIDs = map[string]bool{
	"com.apple.reminders": true,
}

type remindersParser struct{}

func init() {
	Register(&remindersParser{})
}

func (p *remindersParser) CanParse(bundleID string) bool {
	return remindersBundleIDs[bundleID]
}

func (p *remindersParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "reminders",
		Label:       "Apple Reminders",
		ContentType: "task_list",
		Bundles:     []string{"com.apple.reminders"},
	}
}

// ParseTree extracts the visible task list from Reminders.app.
//
// Reminders has a sidebar AXOutline of lists (Today, Scheduled, All, user
// lists) and a main AXScrollArea/AXGroup holding task rows. Each task row
// exposes an AXCheckBox (completion state) plus one or more AXStaticTexts
// (title, notes, due date). The window title usually carries the active
// list name (e.g. "Groceries — Reminders").
func (p *remindersParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}

	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree
	}

	listName := remindersActiveListName(window, windowTitle)
	tasks := remindersCollectTasks(window)
	if len(tasks) == 0 {
		return nil
	}

	body := remindersRenderBody(tasks)
	if strings.TrimSpace(body) == "" {
		return nil
	}

	meta := map[string]string{
		"bundleId": bundleID,
		"app":      "reminders",
	}
	if listName != "" {
		meta["list"] = listName
	}

	title := listName
	if title == "" {
		title = windowTitle
	}

	return &ParsedContent{
		ContentType: "task_list",
		Title:       title,
		Channel:     listName,
		Body:        body,
		Metadata:    meta,
	}
}

// remindersTask captures one reminder row.
type remindersTask struct {
	Completed bool
	Title     string
	Notes     string
	Meta      string // due date / flag / etc rendered as trailing context
}

// remindersActiveListName prefers the leading segment of the window title
// (Reminders renders "List Name — Reminders"), falling back to the sidebar
// outline if no title is set.
func remindersActiveListName(window *AXNode, windowTitle string) string {
	if windowTitle != "" {
		left := strings.Split(windowTitle, " — ")[0]
		left = strings.TrimSpace(left)
		if left != "" && left != "Reminders" {
			return left
		}
	}
	outline := FindFirst(window, func(n *AXNode) bool { return n.Role == "AXOutline" })
	if outline == nil {
		return ""
	}
	rows := FindAll(outline, func(n *AXNode) bool { return n.Role == "AXRow" })
	for _, row := range rows {
		text := FindFirst(row, func(n *AXNode) bool {
			return n.Role == "AXStaticText" && (n.Value != "" || n.Title != "")
		})
		if text == nil {
			continue
		}
		name := text.Value
		if name == "" {
			name = text.Title
		}
		name = strings.TrimSpace(name)
		if name != "" {
			return name
		}
	}
	return ""
}

// remindersCollectTasks finds task rows anywhere below the window.
//
// Strategy: walk every AXCheckBox in the tree and back-pointer up to its
// nearest plausible row container. The container set covers the roles
// macOS Reminders has used across versions — AppKit AXRow / AXGroup,
// SwiftUI AXCell / AXLayoutItem / AXListItem (Reminders on macOS 15+ is
// SwiftUI and no longer wraps each task in AXRow). Starting from the
// checkbox instead of from a fixed container role keeps the parser
// resilient to the next AppKit-vs-SwiftUI shift, since the checkbox
// itself is what disambiguates a reminder row from sidebar / toolbar
// content.
func remindersCollectTasks(window *AXNode) []remindersTask {
	parents := remindersBuildParentMap(window)
	checkboxes := FindAll(window, func(n *AXNode) bool {
		return n.Role == "AXCheckBox"
	})

	seen := map[*AXNode]bool{}
	var tasks []remindersTask
	for _, cb := range checkboxes {
		container := remindersRowContainer(cb, parents)
		if container == nil || seen[container] {
			continue
		}
		seen[container] = true

		texts := remindersRowTexts(container, cb)
		if len(texts) == 0 {
			continue
		}
		task := remindersTask{
			Completed: remindersIsChecked(cb),
			Title:     texts[0],
		}
		if len(texts) > 1 {
			task.Notes = strings.Join(texts[1:], " · ")
		}
		tasks = append(tasks, task)
	}
	return tasks
}

// remindersBuildParentMap returns child→parent for every node under root,
// so callers can walk back up the tree (the AXNode struct only stores
// downward links).
func remindersBuildParentMap(root *AXNode) map[*AXNode]*AXNode {
	parents := map[*AXNode]*AXNode{}
	Traverse(root, func(n *AXNode) bool {
		for _, c := range n.Children {
			if c != nil {
				parents[c] = n
			}
		}
		return true
	})
	return parents
}

// remindersRowContainer walks up from a checkbox looking for its row
// container. Returns nil when the checkbox is inside an inspector
// chrome (Info popover, date picker sheet, …) — those toggles are not
// reminder rows and must not be promoted to tasks. The climb continues
// past the first row-shaped ancestor so a popover further up the chain
// still rejects the checkbox.
func remindersRowContainer(cb *AXNode, parents map[*AXNode]*AXNode) *AXNode {
	var rowContainer *AXNode
	cur := cb
	for range 10 {
		p := parents[cur]
		if p == nil {
			break
		}
		cur = p
		switch p.Role {
		case "AXPopover", "AXSheet", "AXMenu", "AXSystemDialog":
			return nil
		case "AXRow", "AXGroup", "AXCell", "AXLayoutItem", "AXListItem":
			if rowContainer == nil {
				rowContainer = p
			}
		}
	}
	return rowContainer
}

// remindersRowTexts collects the reminder title plus any subtitle text
// for a single row by walking AXStaticText / AXTextField descendants.
//
// Deliberately ignored:
//   - cb.Title / cb.Description — these carry the toggle's action label
//     ("标记为完成" / "Mark as complete"), not the reminder text.
//   - container.Description — Reminders' AXCell exposes a synthesized
//     VoiceOver label that comma-joins state, title, and due time
//     ("未完成、旗标、isoform 早会、提醒、明天 11:00"). It overlaps the
//     individual AXStaticText nodes but as a single string, so we'd
//     emit the title twice in two different shapes and prepend the
//     action label as title. AXStaticText is the authoritative source.
//
// Identical strings are deduped — some macOS builds mirror the title
// across two AXStaticText nodes (e.g. visible label + invisible AT
// label).
func remindersRowTexts(container, cb *AXNode) []string {
	var texts []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		texts = append(texts, s)
	}

	Traverse(container, func(t *AXNode) bool {
		if t == cb {
			return false
		}
		// Skip nested inspector chrome (Info popover, date picker sheet,
		// menus). Without this, opening the (i) detail panel on a
		// reminder pours its entire field list ("日期, 时间, 紧急,
		// 重复, 提前提醒, 列表, 旗标, 优先级, 位置, 发信息时, ...")
		// into the row's subtitle.
		if t != container {
			switch t.Role {
			case "AXPopover", "AXSheet", "AXMenu":
				return false
			}
		}
		switch t.Role {
		case "AXStaticText", "AXTextField":
			v := t.Value
			if v == "" {
				v = t.Description
			}
			if v == "" {
				v = t.Title
			}
			add(v)
		}
		return true
	})
	return texts
}

// remindersIsChecked reads the completion state. Reminders sets the
// checkbox Value to "1" when completed, "0" otherwise; some versions use
// Description "checked" / "unchecked" instead.
func remindersIsChecked(cb *AXNode) bool {
	if cb == nil {
		return false
	}
	if cb.Value == "1" {
		return true
	}
	desc := strings.ToLower(cb.Description)
	return desc == "checked" || strings.Contains(desc, "completed")
}

// remindersRenderBody formats tasks as a plain-text checklist.
func remindersRenderBody(tasks []remindersTask) string {
	var sb strings.Builder
	for i, t := range tasks {
		if i > 0 {
			sb.WriteString("\n")
		}
		mark := "[ ]"
		if t.Completed {
			mark = "[x]"
		}
		sb.WriteString("- ")
		sb.WriteString(mark)
		sb.WriteString(" ")
		sb.WriteString(t.Title)
		if t.Notes != "" {
			sb.WriteString(" — ")
			sb.WriteString(t.Notes)
		}
	}
	return sb.String()
}
