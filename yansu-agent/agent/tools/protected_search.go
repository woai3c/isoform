package tools

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// protectedSearchFS filters before recursion, including when a glob names a
// directory directly instead of reaching it through a parent enumeration.
// Symlinks are omitted, matching the file tools' no-follow contract.
type protectedSearchFS struct {
	root    string
	workDir string
	roots   []string
	home    string
	// boundaries let Grep skip a prewalk of unrelated project folders;
	// ripgrep does not follow symlinks, so it cannot enter a protected root
	// from an otherwise unrelated subtree.
	boundaries []string
	denied     []string
}

func newProtectedSearchFS(root, workDir string) *protectedSearchFS {
	p := &protectedSearchFS{root: root, workDir: workDir, roots: protectedDataRoots(), home: canonicalHome("")}
	p.boundaries = append(p.boundaries, p.roots...)
	for _, data := range p.roots {
		for _, folder := range dataProtectedFolders {
			p.boundaries = append(p.boundaries, credentialPath(filepath.Join(data, folder)))
		}
	}
	if p.home != "" {
		for _, rel := range []string{
			".ssh", "Library/Keychains", ".gnupg", ".aws/credentials", ".config/gh/hosts.yml",
			".netrc", ".docker/config.json", ".kube/config", ".git-credentials",
			"Library/Application Support", "Library/Containers", "Library/Cookies", "Library/Safari",
			".mozilla", ".config", "AppData/Local", "AppData/Roaming",
		} {
			path := filepath.Join(p.home, filepath.FromSlash(rel))
			p.boundaries = append(p.boundaries, path, credentialPath(path))
		}
	}
	return p
}

func (p *protectedSearchFS) blocked(name string) bool {
	path := filepath.Join(p.root, filepath.FromSlash(name))
	canonical, err := canonicalPathLoose(path)
	if err != nil {
		return true
	}
	inside := false
	for _, boundary := range p.boundaries {
		if foldedWithin(canonical, boundary) || foldedWithin(stripFirmlink(path), boundary) {
			inside = true
			break
		}
	}
	if !inside {
		return false
	}
	return protectedHit(canonical, p.workDir, p.roots, true) ||
		(sensitiveFileHit(canonical, p.home) && !sshDirectory(canonical, p.home)) ||
		(sensitiveFileHit(stripFirmlink(path), p.home) && !sshDirectory(stripFirmlink(path), p.home))
}

func (p *protectedSearchFS) mayContainProtected(name string) bool {
	canonical, err := canonicalPathLoose(filepath.Join(p.root, filepath.FromSlash(name)))
	if err != nil {
		return true
	}
	for _, boundary := range p.boundaries {
		if foldedWithin(canonical, boundary) || foldedWithin(boundary, canonical) {
			return true
		}
	}
	return false
}

func (p *protectedSearchFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) || p.blocked(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return os.DirFS(p.root).Open(name)
}

func (p *protectedSearchFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) || p.blocked(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	entries, err := os.ReadDir(filepath.Join(p.root, filepath.FromSlash(name)))
	if err != nil {
		return nil, err
	}
	allowed := entries[:0]
	for _, entry := range entries {
		rel := filepath.ToSlash(filepath.Join(name, entry.Name()))
		if entry.Type()&os.ModeSymlink != 0 || p.blocked(rel) {
			p.denied = append(p.denied, rel)
			continue
		}
		allowed = append(allowed, entry)
	}
	return allowed, nil
}

// protectedGrepExclusions walks with the same boundary as Glob, then gives
// ripgrep literal negative globs for the pruned entries. Ripgrep still owns
// gitignore, type/glob filters, regex semantics and output formatting. Denies
// are appended after user filters, so an include cannot override them.
func protectedGrepExclusions(ctx context.Context, root, workDir string) ([]string, error) {
	// rg follows an explicitly named root symlink even with --no-follow.
	// Inspect its target so a link to an ancestor still receives pruning.
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	fsys := newProtectedSearchFS(root, workDir)
	err = fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			// Exclude unreadable subtrees rather than passing a path whose
			// contents we could not check to a separate recursive reader.
			fsys.denied = append(fsys.denied, path)
			return fs.SkipDir
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules") {
			fsys.denied = append(fsys.denied, path)
			return fs.SkipDir
		}
		if entry.IsDir() && !fsys.mayContainProtected(path) {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var globs []string
	escape := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`, `{`, `\{`, `}`, `\}`)
	for _, path := range fsys.denied {
		// The ** prefix works for both relative and absolute rg roots.
		// Escaping every metacharacter prevents unusual filenames from
		// widening an exclusion or allowing the intended target through.
		globs = append(globs, "!**/"+escape.Replace(path))
	}
	return globs, nil
}
