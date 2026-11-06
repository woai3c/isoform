package main

import "context"

// registryPeopleWorkTopics adapts the RC 1 A1 topic registry
// (topics_registry.go) to the peopleWorkTopics seam. It reads the registry's
// committed snapshot — the topics.json projects published under the
// registry's own source and privacy barriers — and, like the chat admission
// reader, never rebuilds or writes during a read. Every topic it returns was
// shown by the registry at the last validated publish: withheld projects
// persist as content-free reservations, and Closed Eyes, sensitive and
// private sources never reach a published project. Person members are the
// project record's persisted "person:<name>" keys, derived from the same
// gated observations as the view's PeopleLine. Names are never invented here.
type registryPeopleWorkTopics struct {
	app *App
}

func (r registryPeopleWorkTopics) ReadableWorkTopics(ctx context.Context) ([]peopleWorkTopic, error) {
	if r.app == nil || r.app.IsClosedEyesModeEnabled() {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := []peopleWorkTopic{}
	for _, project := range r.app.topicsSvc().store.load().Projects {
		if project.Name == "" || len(project.PeopleKeys) == 0 {
			continue
		}
		out = append(out, peopleWorkTopic{ID: project.ID, Name: project.Name, Keys: append([]string(nil), project.PeopleKeys...), ActivityIDs: append([]string(nil), project.ActivityIDs...), RequireEvidence: true})
	}
	return out, nil
}
