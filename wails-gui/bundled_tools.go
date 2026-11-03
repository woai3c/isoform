package main

import "path/filepath"

// toolBundleCandidates covers packaged macOS Resources, portable distributions,
// and a developer's staged bundle. Discovery itself never installs anything.
func toolBundleCandidates(executable, workingDir, bundle string, relative ...string) []string {
	var candidates []string
	if executable != "" {
		exeDir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(append([]string{exeDir, bundle}, relative...)...),
			filepath.Join(append([]string{exeDir, "..", "Resources", bundle}, relative...)...),
		)
	}
	if workingDir != "" {
		candidates = append(candidates, filepath.Join(append([]string{workingDir, bundle}, relative...)...))
	}
	return candidates
}
