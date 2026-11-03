package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"wails-gui/storage"
)

// Transcript headers and recording sidecars carry read provenance. Their
// on-disk withdrawal must invalidate a view cache even without a SQL write.
type topicSessionFileRevision struct {
	Paths []string
	Stats topicFileStats
}

// Reuse only the enumerated dependency identities, never their authorization.
// Any SQL change or header/sidecar edit requires enumeration outside fences.
func (s *topicsService) registrySessionFilesForCapture(ctx context.Context, sources topicActivityRevision) (topicSessionFileRevision, error) {
	if c := s.loadRegistryCache(); c != nil && c.revision.activityStore != nil && s.app.activity != nil && s.app.activity.store == c.revision.activityStore && c.revision.activitySources == sources {
		revision, err := storage.DatabaseRevision(ctx, c.revision.activityStore.db)
		if err != nil {
			return topicSessionFileRevision{}, err
		}
		if revision == c.revision.activity {
			stats, err := topicSessionFileStats(ctx, c.revision.sessionFiles.Paths)
			if err != nil {
				return topicSessionFileRevision{}, err
			}
			if stats == c.revision.sessionFiles.Stats {
				return c.revision.sessionFiles, nil
			}
		}
	}
	return s.registrySessionFiles(ctx)
}

const topicRecordingHeaderLimit = 1 << 20

func (s *topicsService) registrySessionFiles(ctx context.Context) (topicSessionFileRevision, error) {
	var result topicSessionFileRevision
	if s.app.activity == nil || s.app.activity.store == nil {
		return result, nil
	}
	store := s.app.activity.store
	rows, err := store.reader().QueryContext(ctx, `SELECT COALESCE(transcript_path,''),COALESCE(audio_path,'') FROM activity_sessions WHERE transcript_path<>'' OR audio_path<>''`)
	if err != nil {
		return result, err
	}
	paths := map[string]bool{}
	headers := map[string]bool{}
	for rows.Next() {
		var transcript, audio string
		if err := rows.Scan(&transcript, &audio); err != nil {
			rows.Close()
			return result, err
		}
		if transcript != "" {
			paths[transcript] = true
			if audio == "" {
				headers[transcript] = true
			}
		}
		if audio != "" {
			paths[meetingWAVMetaPath(store.AudioDir(), audio)] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	return topicRecordingFileSnapshot(ctx, store.AudioDir(), paths, headers, s.registryRecordingHeadersRead)
}

// Retention can clear audio_path while its transcript header still names the
// sidecar used by transcriptSourceIDs. Bind that permission file too. Only a
// streaming header is inspected; its text never becomes view/model content.
func topicRecordingFileSnapshot(ctx context.Context, audioDir string, paths, headers map[string]bool, afterHeaders func()) (topicSessionFileRevision, error) {
	original := sortedTopicKeys(paths)
	before, err := topicSessionFileStats(ctx, original)
	if err != nil {
		return topicSessionFileRevision{}, err
	}
	for _, path := range sortedTopicKeys(headers) {
		if err := ctx.Err(); err != nil {
			return topicSessionFileRevision{}, err
		}
		file, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return topicSessionFileRevision{}, err
		}
		reader := bufio.NewReader(io.LimitReader(file, topicRecordingHeaderLimit+1))
		lineStart := true
		read := 0
		for {
			part, readErr := reader.ReadSlice('\n')
			read += len(part)
			if read > topicRecordingHeaderLimit {
				err = errTopicRegistryChanged
				break
			}
			if err = ctx.Err(); err != nil {
				break
			}
			if lineStart {
				line := strings.TrimSuffix(string(part), "\n")
				if strings.Contains(line, "## Transcript") {
					break
				}
				if ref, ok := strings.CutPrefix(line, "- **Audio**: activity/audio/"); ok {
					if readErr == bufio.ErrBufferFull || !filepath.IsLocal(ref) {
						err = errTopicRegistryChanged
						break
					}
					paths[meetingWAVMetaPath(audioDir, ref)] = true
					break
				}
			}
			// Long evidence-ID lines need no allocation or parsing here. Keep
			// scanning fragments under both the deadline and the header byte
			// cap. An unresolved over-cap dependency closure fails closed.
			lineStart = readErr != bufio.ErrBufferFull
			if readErr != nil && readErr != bufio.ErrBufferFull {
				if readErr != io.EOF {
					err = readErr
				}
				break
			}
		}
		file.Close()
		if err != nil {
			return topicSessionFileRevision{}, err
		}
	}
	after, err := topicSessionFileStats(ctx, original)
	if err != nil {
		return topicSessionFileRevision{}, err
	}
	if before != after {
		return topicSessionFileRevision{}, errTopicRegistryChanged
	}
	if afterHeaders != nil {
		afterHeaders()
	}
	result := topicSessionFileRevision{Paths: sortedTopicKeys(paths)}
	result.Stats, err = topicSessionFileStats(ctx, result.Paths)
	if err != nil {
		return result, err
	}
	// The final combined stat must still describe the header version parsed
	// above. Otherwise it could bind a new header to the old sidecar path.
	final, err := topicSessionFileStats(ctx, original)
	if err == nil && final != before {
		err = errTopicRegistryChanged
	}
	return result, err
}

// The final publication check runs under database fences: file stats only,
// with the exact path identities captured before the database was fenced.
func topicSessionFileStats(ctx context.Context, paths []string) (topicFileStats, error) {
	stats := make([]topicFileStat, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return topicFileStats{}, err
		}
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			stats = append(stats, topicFileStat{Path: path})
			continue
		}
		if err != nil {
			return topicFileStats{}, err
		}
		stat, err := topicSourceFileStat(path, info)
		if err != nil {
			return topicFileStats{}, err
		}
		stats = append(stats, stat)
	}
	raw, err := json.Marshal(stats)
	if err != nil {
		return topicFileStats{}, err
	}
	return topicFileStats(sha256.Sum256(raw)), nil
}
