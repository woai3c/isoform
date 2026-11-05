package main

import (
	"encoding/json"
	"strings"
	"time"
)

// Who you know shows the open follow-ups Activity's "Isoform noticed" box
// suggests for a person (launch walk N48). A suggestion is not an open loop
// until the user adds it, so without this line the person row said nothing
// was open even though Activity had asked the user to follow up with them.

// peopleNoticedLine is one day's follow-up suggestion and the people its moment names.
type peopleNoticedLine struct {
	ID    string
	Text  string
	Since int64
	Names []string
}

// peopleNoticedFollowUps reads today's and yesterday's checked suggestions with
// the same rules the Activity box uses (current generation, dismissals, open
// follow-ups and privacy). It only reads; it never emits events.
func (a *App) peopleNoticedFollowUps(now time.Time) []peopleNoticedLine {
	if a.activity == nil || a.activity.store == nil {
		return nil
	}
	language := a.currentUILanguage()
	var out []peopleNoticedLine
	for back := 0; back < 2; back++ {
		date := now.AddDate(0, 0, -back).Format("2006-01-02")
		items, generation, err := a.activity.store.noticedStored(date, language)
		if err != nil || len(items) == 0 || !a.noticedCurrent(date, language, generation) {
			continue
		}
		checked, err := a.noticedCheck(date, items)
		if err != nil {
			continue
		}
		var follow []NoticedSuggestion
		for _, item := range checked {
			if item.Kind == noticedKindFollowUp {
				follow = append(follow, item)
			}
		}
		if len(follow) == 0 {
			continue
		}
		moments, _, err := a.noticedCandidateMoments(date, follow)
		if err != nil {
			continue
		}
		byID := make(map[string]ActivityRow, len(moments))
		for _, m := range moments {
			byID[m.ID] = m
		}
		for _, item := range follow {
			row, ok := byID[item.MomentID]
			if !ok {
				continue
			}
			if names := personEntityNames(row.Entities); len(names) > 0 {
				out = append(out, peopleNoticedLine{ID: item.ID, Text: item.Text, Since: row.StartMs, Names: names})
			}
		}
	}
	return out
}

// personEntityNames are the person names a moment's entities list, normalized.
func personEntityNames(raw string) []string {
	var entities []Entity
	if json.Unmarshal([]byte(raw), &entities) != nil {
		return nil
	}
	var out []string
	for _, e := range entities {
		if e.Type == "person" {
			if name := threadNorm(e.Name); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// peopleFirstNames counts how many people share each first name, so a bare
// first name matches a person only when no one else has it.
func peopleFirstNames(displayNames map[string]string) map[string]int {
	counts := map[string]int{}
	for _, name := range displayNames {
		if first := personFirstName(name); first != "" {
			counts[first]++
		}
	}
	return counts
}

func personFirstName(name string) string {
	fields := strings.Fields(threadNorm(name))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// noticedNamesPerson reports whether a named person in a moment is this person:
// the full name or an alias, or a bare first name that only this person has.
func noticedNamesPerson(entityName string, names map[string]bool, displayName string, firstCounts map[string]int) bool {
	if names[entityName] {
		return true
	}
	return !strings.Contains(entityName, " ") && firstCounts[entityName] == 1 && personFirstName(displayName) == entityName
}
