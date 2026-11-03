// Package recentfiles finds the documents the user opened or edited lately
// through Spotlight's metadata (kMDItemLastUsedDate,
// kMDItemContentModificationDate) in the home folder's Documents, Desktop
// and Downloads and in iCloud Drive. It reads metadata only: no file is
// opened, listed or read, and no content leaves Spotlight's index.
//
// The rules that decide what is never kept (hidden files, caches,
// node_modules, Library, the inside of packages, files that hold secrets,
// the folders the user keeps private) and what counts as work (an open, or
// an edit made at least a minute after the file arrived) are pure and live
// here, table-tested; the reader that applies them is
// wails-gui/files_activity_sync.go.
package recentfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Root keys: the folders the reader looks in.
const (
	RootDocuments = "documents"
	RootDesktop   = "desktop"
	RootDownloads = "downloads"
	RootICloud    = "icloud"
)

// What the user did with a file.
const (
	ActionOpened = "opened"
	ActionEdited = "edited"
)

// File kinds, from the content type.
const (
	KindDocument     = "document"
	KindSpreadsheet  = "spreadsheet"
	KindPresentation = "presentation"
	KindPDF          = "pdf"
	KindImage        = "image"
	KindText         = "text"
	KindCode         = "code"
	KindOther        = "other"
)

// editedAfterArrival is how long after a file was created or added an edit
// must come to count as the user's work, so a download or a copy that takes
// a few seconds to finish is not taken for an edit.
const editedAfterArrival = time.Minute

// Root is one folder the reader looks in.
type Root struct {
	Key  string
	Path string
}

// Roots lists the folders under home, in display order.
func Roots(home string) []Root {
	if home == "" {
		return nil
	}
	return []Root{
		{Key: RootDocuments, Path: filepath.Join(home, "Documents")},
		{Key: RootDesktop, Path: filepath.Join(home, "Desktop")},
		{Key: RootDownloads, Path: filepath.Join(home, "Downloads")},
		{Key: RootICloud, Path: filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")},
	}
}

// Item is what Spotlight reports about one file: its path and name, its
// content type, and its dates. Never its contents.
type Item struct {
	Path        string
	Name        string
	ContentType string
	TypeTree    []string
	LastUsed    time.Time
	Modified    time.Time
	Created     time.Time
	Added       time.Time
}

// rawItem is one entry of the bridge's JSON. Dates are Unix milliseconds,
// 0 when Spotlight has none.
type rawItem struct {
	Path     string   `json:"path"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Tree     []string `json:"tree"`
	LastUsed int64    `json:"lastUsed"`
	Modified int64    `json:"modified"`
	Created  int64    `json:"created"`
	Added    int64    `json:"added"`
}

// ParseItems maps the bridge's JSON to items. An entry without a path is
// dropped; a missing name falls back to the path's last element.
func ParseItems(data []byte) ([]Item, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var raws []rawItem
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("parse spotlight results: %w", err)
	}
	items := make([]Item, 0, len(raws))
	for _, raw := range raws {
		path := strings.TrimSpace(raw.Path)
		if path == "" {
			continue
		}
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			name = filepath.Base(path)
		}
		items = append(items, Item{
			Path:        path,
			Name:        name,
			ContentType: raw.Type,
			TypeTree:    raw.Tree,
			LastUsed:    millis(raw.LastUsed),
			Modified:    millis(raw.Modified),
			Created:     millis(raw.Created),
			Added:       millis(raw.Added),
		})
	}
	return items, nil
}

func millis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// RecentQuery is the Spotlight query for files opened or changed at or after
// since. Folders are told apart by Skipped, since a not-equal test on the
// multi-valued type tree is not reliable.
func RecentQuery(since time.Time) string {
	at := since.UTC().Format("2006-01-02T15:04:05Z")
	return fmt.Sprintf(`kMDItemLastUsedDate >= $time.iso(%s) || kMDItemContentModificationDate >= $time.iso(%s)`, at, at)
}

// ProbeQuery matches any item; one result in a folder shows Spotlight lets
// Isoform see that folder.
const ProbeQuery = `kMDItemContentTypeTree == "public.item"`

// Canonical maps a path inside iCloud Drive's Desktop or Documents folder
// (where "Desktop & Documents Folders" keeps them) to the same path under
// the home folder, so a document has one identity, one root and one answer
// to the private folders whichever path Spotlight reports.
func Canonical(path, home string) string {
	if home == "" {
		return filepath.Clean(path)
	}
	cloud := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")
	for _, name := range []string{"Desktop", "Documents"} {
		if rel, ok := relativeTo(path, filepath.Join(cloud, name)); ok {
			return filepath.Join(home, name, rel)
		}
	}
	return filepath.Clean(path)
}

// RootOf returns the root a path lies under and the path relative to it.
func RootOf(roots []Root, path string) (Root, string, bool) {
	for _, root := range roots {
		if rel, ok := relativeTo(path, root.Path); ok && rel != "." {
			return root, rel, true
		}
	}
	return Root{}, "", false
}

// relativeTo reports path relative to dir when path is dir or inside it.
// The comparison ignores case, as the Mac's file system does.
func relativeTo(path, dir string) (string, bool) {
	path = filepath.Clean(path)
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return "", false
	}
	if strings.EqualFold(path, dir) {
		return ".", true
	}
	prefix := dir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if len(path) <= len(prefix) || !strings.EqualFold(path[:len(prefix)], prefix) {
		return "", false
	}
	return path[len(prefix):], true
}

// UnderFolder reports whether path is folder or lies inside it.
func UnderFolder(path, folder string) bool {
	_, ok := relativeTo(path, folder)
	return ok
}

// skippedDirs are folders whose files are never kept, wherever they sit
// below a root: what tools and apps keep for themselves, not documents.
// Names a person might give a folder of their own ("Build", "Target") are
// not on the list.
var skippedDirs = map[string]bool{
	"library":      true,
	"node_modules": true,
	"caches":       true,
	"cache":        true,
	"deriveddata":  true,
	"__pycache__":  true,
}

// buildDirs are the folders build tools write their output to, rebuilt in
// place on every build. Only the lowercase names the tools write are
// skipped, so a person's own "Build" or "Target" folder stays.
var buildDirs = map[string]bool{
	"build":  true,
	"dist":   true,
	"target": true,
}

// buildSuffixes are files a compiler writes, never a document.
var buildSuffixes = []string{
	".o", ".a", ".so", ".dylib", ".class", ".pyc", ".rlib", ".rmeta", ".js.map", ".css.map",
}

// packageSuffixes mark folders macOS shows as one file whose insides are the
// app's own data, never the user's documents.
var packageSuffixes = []string{
	".app", ".framework", ".bundle", ".plugin", ".kext",
	".photoslibrary", ".musiclibrary", ".tvlibrary", ".photolibrary",
	".xcodeproj", ".xcworkspace", ".xcassets", ".xcarchive",
	".sparsebundle", ".logarchive",
}

// Skipped reports whether a file, by its path relative to its root and its
// type tree, is never kept: hidden, inside a hidden, skipped or build output
// folder or a package, a folder itself, an app, or a compiler's output.
func Skipped(rel string, tree []string) bool {
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return true
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasPrefix(part, "~$") {
			return true
		}
		if i == len(parts)-1 {
			break
		}
		lower := strings.ToLower(part)
		if skippedDirs[lower] || buildDirs[part] {
			return true
		}
		for _, suffix := range packageSuffixes {
			if strings.HasSuffix(lower, suffix) {
				return true
			}
		}
	}
	return notADocument(parts[len(parts)-1], tree)
}

// notADocument reports a folder, an app, a partial download or a
// compiler's output.
func notADocument(name string, tree []string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".app", ".download", ".crdownload", ".part", ".partial", ".tmp", ".icloud"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	for _, suffix := range buildSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	isPackage := false
	for _, t := range tree {
		switch t {
		case "com.apple.package":
			isPackage = true
		case "public.folder", "public.volume", "com.apple.application", "com.apple.application-bundle":
			return true
		}
	}
	// A plain directory that is not a package (a Pages or Keynote file is
	// one) is a folder.
	for _, t := range tree {
		if t == "public.directory" && !isPackage {
			return true
		}
	}
	return false
}

// secretExtensions are files that hold passwords, keys or wallets.
var secretExtensions = map[string]bool{
	".kdbx": true, ".kdb": true, ".1pux": true, ".1pif": true, ".opvault": true,
	".agilekeychain": true, ".keychain": true, ".keychain-db": true, ".psafe3": true,
	".pem": true, ".p12": true, ".pfx": true, ".cer": true, ".crt": true,
	".gpg": true, ".asc": true, ".pgp": true, ".ovpn": true, ".mobileprovision": true,
	".wallet": true,
}

// secretName matches a file name that says it holds a secret. Separators
// are turned into spaces first, so "my_passwords.csv" matches.
var secretName = regexp.MustCompile(`(?i)\b(?:pass(?:word|wd|phrase|code)s?|pwd|pin|secrets?|mnemonic|credentials?|verification[ ]*codes?|(?:seed|recovery|backup)[ ]?(?:phrase|words?|keys?|codes?)|private[ ]?keys?|api[ ]?keys?|(?:access|auth|api|bearer|refresh)[ ]?tokens?|id[ ]?rsa|id[ ]?ed25519|wallet|keystore|2fa|otp)\b|密码|口令|助记词|验证码|私钥|恢复短语|钱包`)

// SecretName reports whether a file's name or extension says it holds
// passwords, verification codes, keys or a wallet; such a file is never kept. (.key is
// Keynote's extension too, so it is not on the list.)
func SecretName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if secretExtensions[filepath.Ext(lower)] {
		return true
	}
	spaced := strings.Map(func(r rune) rune {
		switch r {
		case '_', '-', '.', '+', '(', ')', '[', ']':
			return ' '
		}
		return r
	}, lower)
	return secretName.MatchString(spaced)
}

// Kind classifies a file by its content type and type tree.
func Kind(contentType string, tree []string) string {
	has := map[string]bool{contentType: true}
	for _, t := range tree {
		has[t] = true
	}
	switch {
	case has["com.adobe.pdf"]:
		return KindPDF
	case has["public.presentation"] || has["com.apple.iwork.keynote.key"] || has["com.apple.keynote.key"]:
		return KindPresentation
	case has["public.spreadsheet"] || has["com.apple.iwork.numbers.numbers"] || has["com.apple.iwork.numbers.sffnumbers"]:
		return KindSpreadsheet
	case has["public.image"]:
		return KindImage
	case has["public.source-code"] || has["public.script"] || has["public.shell-script"]:
		return KindCode
	case has["public.composite-content"] || has["com.apple.iwork.pages.pages"] || has["com.apple.iwork.pages.sffpages"] ||
		has["org.openxmlformats.wordprocessingml.document"] || has["com.microsoft.word.doc"] || has["public.rtf"]:
		return KindDocument
	case has["public.text"] || has["public.plain-text"]:
		return KindText
	default:
		return KindOther
	}
}

// Activity is what the user last did with a file and when: an edit, made at
// least a minute after the file was created or added, or an open. A file in
// Downloads counts only when opened, since arriving there is not the user's
// work. ok is false when neither happened.
func Activity(item Item, rootKey string) (at time.Time, action string, ok bool) {
	edited := time.Time{}
	if rootKey != RootDownloads && !item.Modified.IsZero() {
		arrived := item.Created
		if item.Added.After(arrived) {
			arrived = item.Added
		}
		if !arrived.IsZero() && item.Modified.Sub(arrived) >= editedAfterArrival {
			edited = item.Modified
		}
	}
	switch {
	case !edited.IsZero() && !edited.Before(item.LastUsed):
		return edited, ActionEdited, true
	case !item.LastUsed.IsZero():
		return item.LastUsed, ActionOpened, true
	default:
		return time.Time{}, "", false
	}
}

// PathHash is the stored identity of a path: a hash of its lower-cased form,
// so the same file always gets the same id and no path is stored.
func PathHash(path string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(path))))
	return hex.EncodeToString(sum[:16])
}

// FolderHash is the stored identity of one folder above a file.
func FolderHash(dir string) string {
	sum := sha256.Sum256([]byte("dir:" + strings.ToLower(filepath.Clean(dir))))
	return hex.EncodeToString(sum[:8])
}

// AncestorHashes are the FolderHash of every folder above path, up to and
// including the top of the disk, joined by spaces. A folder the user later
// keeps private finds its files by its own hash among them.
func AncestorHashes(path string) string {
	var hashes []string
	dir := filepath.Dir(filepath.Clean(path))
	for {
		hashes = append(hashes, FolderHash(dir))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return strings.Join(hashes, " ")
}
