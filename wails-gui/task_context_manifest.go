package main

func taskContextSourceExclusionManifest(
	exclusions []TaskContextSourceExclusion,
) []TaskContextManifestEntry {
	result := make([]TaskContextManifestEntry, 0, len(exclusions))
	for _, exclusion := range exclusions {
		result = append(result, TaskContextManifestEntry{
			Ref:        exclusion.Ref,
			SourceKind: exclusion.SourceKind,
			SourceID:   exclusion.SourceID,
			ObservedAt: exclusion.ObservedAt,
			Included:   false,
			Reason:     exclusion.Reason,
			Learning:   exclusion.Learning,
		})
	}
	return result
}
