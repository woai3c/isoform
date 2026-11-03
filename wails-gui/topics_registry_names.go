package main

import (
	"context"
	"encoding/json"
	"time"
)

// A name exclusion needs names, not activity provenance, goal evidence, chat
// transcripts or the full interest ranking input. Reading extra names here can
// only withhold a label; this input never supplies topic text or permission.
func (s *topicsService) refreshRegistryNames(ctx context.Context, now time.Time) error {
	names := []string{}
	allDone := timeTopicStep("registry_step", "refresh_names")
	defer func() { allDone("names", len(names)) }()
	if s.app.store == nil && (s.app.activity == nil || s.app.activity.store == nil) {
		return nil
	}
	if s.app.store != nil {
		principalsDone := timeTopicStep("registry_step", "names_principals_sql")
		people, err := s.app.store.ListPrincipals(ctx)
		principalsDone("rows", len(people))
		if err != nil {
			return err
		}
		aliasesDone := timeTopicStep("registry_step", "names_principal_aliases")
		aliases := 0
		defer func() { aliasesDone("rows", len(people), "aliases", aliases) }()
		for _, person := range people {
			names = append(names, person.DisplayName)
			names = append(names, person.Aliases...)
			aliases += len(person.Aliases)
		}
		aliasesDone("rows", len(people), "aliases", aliases)
		calendarDone := timeTopicStep("registry_step", "names_calendar_sql")
		events, err := s.app.store.ListCalendarEvents(ctx, now.Add(-topicWindow).UnixMilli(), now.Add(topicUpcomingAhead).UnixMilli(), false)
		calendarDone("rows", len(events))
		if err != nil {
			return err
		}
		attendeesDone := timeTopicStep("registry_step", "names_calendar_attendees")
		attendees := 0
		defer func() { attendeesDone("rows", len(events), "attendees", attendees) }()
		for _, event := range events {
			names = append(names, event.OrganizerName, event.OrganizerEmail)
			for _, attendee := range event.Attendees {
				names = append(names, attendee.Name, attendee.Email)
				attendees++
			}
		}
		attendeesDone("rows", len(events), "attendees", attendees)
	}
	if s.app.activity != nil && s.app.activity.store != nil {
		queryDone := timeTopicStep("registry_step", "names_entities_sql")
		rows, err := s.app.activity.store.reader().QueryContext(ctx, `SELECT entities FROM activities WHERE end_ms>=? AND start_ms<=? UNION ALL SELECT entities FROM activity_semantic_events WHERE end_ms>=? AND start_ms<=?`, now.Add(-topicWindow).UnixMilli(), now.UnixMilli(), now.Add(-topicWindow).UnixMilli(), now.UnixMilli())
		queryDone()
		if err != nil {
			return err
		}
		rowsDone := timeTopicStep("registry_step", "names_entities_rows")
		rowCount, entityCount := 0, 0
		defer func() { rowsDone("rows", rowCount, "entities", entityCount) }()
		for rows.Next() {
			rowCount++
			if err := ctx.Err(); err != nil {
				rows.Close()
				return err
			}
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			if raw == "" {
				continue
			}
			var entities []Entity
			if err := json.Unmarshal([]byte(raw), &entities); err != nil {
				rows.Close()
				return err
			}
			entityCount += len(entities)
			for _, entity := range entities {
				if entity.Type == entityPerson {
					names = append(names, entity.Name)
				}
			}
		}
		err = rows.Err()
		rows.Close()
		rowsDone("rows", rowCount, "entities", entityCount)
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rememberDone := timeTopicStep("registry_step", "names_remember")
	s.app.interestsSvc().rememberNames(names, now)
	rememberDone("names", len(names))
	return nil
}
