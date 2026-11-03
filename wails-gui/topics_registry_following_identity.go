package main

import "wails-gui/internal/interests"

// Display labels and mere evidence overlap never join identities. A follow
// refers to the original admitted member identifier, before display naming.
func topicFollowingProjectIDs(builds []topicProjectBuild, following UserTopicsView) map[string]string {
	ids := map[string]string{}
	for _, followed := range following.Topics {
		if followed.Kind != string(interests.KindProject) {
			continue
		}
		for _, build := range builds {
			if topicProjectMatchesFollowingMember(build, followed) {
				ids[followed.ID] = build.project.ID
				break
			}
		}
	}
	return ids
}

func topicProjectMatchesFollowingMember(build topicProjectBuild, followed UserTopic) bool {
	name := topicInterestName(followed)
	for _, activity := range build.activities {
		for _, member := range activity.KeyNames {
			if topicNamesEqual(name, member) {
				return true
			}
		}
	}
	for _, goal := range build.goals {
		if topicNamesEqual(name, goal.Title) {
			return true
		}
	}
	return false
}
