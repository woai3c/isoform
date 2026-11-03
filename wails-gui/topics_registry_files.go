package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// File identity/mtime/size checks are cheap publication revisions. Content
// digests are captured separately, without holding privacy or memory barriers.
type topicFileStats [sha256.Size]byte

type topicFileStat struct {
	Path     string
	Size     int64
	Modified int64
	Mode     uint32
	Identity string
}

func (s *topicsService) registrySourceFileStats(ctx context.Context) (topicFileStats, error) {
	started := time.Now()
	if s.registryFileStats != nil {
		defer func() { s.registryFileStats(time.Since(started)) }()
	}
	var empty topicFileStats
	memory, err := memoryDir()
	if err != nil {
		return empty, err
	}
	knowledge, err := knowledgeDir()
	if err != nil {
		return empty, err
	}
	stats := []topicFileStat{}
	for _, dir := range []string{memory, knowledge, filepath.Join(memory, ".semantic")} {
		info, err := os.Lstat(dir)
		if os.IsNotExist(err) {
			stats = append(stats, topicFileStat{Path: dir})
			continue
		}
		if err != nil {
			return empty, err
		}
		if !info.IsDir() {
			return empty, errTodayChatItemUnavailable
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return empty, err
		}
		// Directory mtime also catches a concurrent create/remove during listing.
		dirStat, err := topicSourceFileStat(dir, info)
		if err != nil {
			return empty, err
		}
		stats = append(stats, dirStat)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return empty, err
			}
			if entry.IsDir() {
				continue
			}
			switch strings.ToLower(filepath.Ext(entry.Name())) {
			case ".md", ".json", ".jsonl":
			default:
				continue
			}
			path := filepath.Join(dir, entry.Name())
			info, err := os.Lstat(path)
			if err != nil {
				return empty, err
			}
			if !info.Mode().IsRegular() {
				return empty, errTodayChatItemUnavailable
			}
			stat, err := topicSourceFileStat(path, info)
			if err != nil {
				return empty, err
			}
			stats = append(stats, stat)
		}
	}
	bytes, err := json.Marshal(stats)
	if err != nil {
		return empty, err
	}
	return topicFileStats(sha256.Sum256(bytes)), ctx.Err()
}

func (s *topicsService) hashRegistrySourceFiles(ctx context.Context) (todayChatAdmissionFileRevision, topicFileStats, error) {
	var empty todayChatAdmissionFileRevision
	before, err := s.registrySourceFileStats(ctx)
	if err != nil {
		return empty, before, err
	}
	if s.registryFilesHashed != nil {
		s.registryFilesHashed()
	}
	files, err := s.app.readTodayChatSourceFiles(ctx, TodayChatContextRef{SourceKind: "topic_registry"}, false)
	if err != nil {
		return empty, before, err
	}
	after, err := s.registrySourceFileStats(ctx)
	if err != nil {
		return empty, before, err
	}
	if after != before {
		return empty, before, errTopicRegistryChanged
	}
	return files, after, nil
}

// Darwin and Linux expose nanosecond change time and inode identity through
// FileInfo.Sys. Exclude access time (hashing reads may change it), and refuse
// an unsupported stat layout instead of silently weakening the source fence.
func topicSourceFileStat(path string, info os.FileInfo) (topicFileStat, error) {
	stat := topicFileStat{Path: path, Size: info.Size(), Modified: info.ModTime().UnixNano(), Mode: uint32(info.Mode())}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer && !value.IsNil() {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return stat, errTopicRegistryChanged
	}
	parts := []string{}
	changed := false
	for _, name := range []string{"Dev", "Ino", "Ctim", "Ctimespec", "Ctime", "Ctimensec"} {
		field := value.FieldByName(name)
		if !field.IsValid() || !field.CanInterface() {
			continue
		}
		parts = append(parts, name+":"+fmt.Sprint(field.Interface()))
		changed = changed || name == "Ctim" || name == "Ctimespec" || name == "Ctimensec"
	}
	if !changed {
		return stat, errTopicRegistryChanged
	}
	stat.Identity = strings.Join(parts, "/")
	return stat, nil
}

func topicMarkdownRevision(kind, id string) (string, error) {
	if id == "" || strings.Contains(id, "..") || strings.ContainsAny(id, "/\\") {
		return "", errTodayChatItemUnavailable
	}
	var dir string
	var err error
	switch kind {
	case "memory":
		dir, err = memoryDir()
	case "knowledge":
		dir, err = knowledgeDir()
	default:
		return "", errTodayChatItemUnavailable
	}
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".md")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errTodayChatItemUnavailable
	}
	stat, err := topicSourceFileStat(path, info)
	if err != nil {
		return "", err
	}
	return proactiveVoiceHash(stat), nil
}

// Content hashes can be reused only while the full inode/change-time metadata
// matches. This avoids rereading every memory/knowledge file on a warm read.
type topicRegistryFileCache struct {
	stats topicFileStats
	files todayChatAdmissionFileRevision
}

func (s *topicsService) cachedRegistrySourceFiles(ctx context.Context) (todayChatAdmissionFileRevision, topicFileStats, error) {
	stats, err := s.registrySourceFileStats(ctx)
	if err != nil {
		return todayChatAdmissionFileRevision{}, stats, err
	}
	s.registryFilesMu.Lock()
	cached := s.registryFilesCache
	s.registryFilesMu.Unlock()
	if cached != nil && cached.stats == stats && s.registryFilesHashed == nil {
		return cached.files, stats, nil
	}
	files, stats, err := s.hashRegistrySourceFiles(ctx)
	if err == nil {
		s.registryFilesMu.Lock()
		s.registryFilesCache = &topicRegistryFileCache{stats: stats, files: files}
		s.registryFilesMu.Unlock()
	}
	return files, stats, err
}
