package skills

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/userdir"
)

// Pinned install (agent-platform Phase 2): a skill from a local folder or a
// git commit is copied into quarantine, hashed and shown to a person; only
// after they approve is it moved into the workspace and recorded, with the
// hash of every file, in skills.lock. A locked skill whose files later
// change is reported as tampered and not offered to the model.

// Install limits keep a hostile source from filling the disk or the context.
const (
	maxInstallFiles = 200
	maxInstallBytes = 4 << 20
)

// File is one file in a skill tree.
type File struct {
	Path   string `json:"path"` // slash-separated, relative to the skill folder
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Fetched is a skill copied into quarantine, not yet installed.
type Fetched struct {
	Skill    Skill
	Dir      string // quarantine folder
	Source   string
	TreeHash string
	Files    []File
}

// Remove deletes the quarantine copy, including its per-fetch folder.
func (f Fetched) Remove() error {
	if f.Dir == "" {
		return nil
	}
	return os.RemoveAll(filepath.Dir(f.Dir))
}

// QuarantineDir is where fetched skills wait for approval.
func QuarantineDir(userConfigDir string) string {
	return filepath.Join(userdir.App(userConfigDir), "skills-quarantine")
}

// LockPath is the install record.
func LockPath(userConfigDir string) string {
	return filepath.Join(userdir.App(userConfigDir), "skills.lock")
}

var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Fetch copies source into a new folder under quarantineRoot and checks it is
// a valid skill. source is a local folder holding SKILL.md, or
// "https://host/repo.git@<40-hex commit>" (a full commit, so the content is
// pinned; branches and tags can move).
func Fetch(ctx context.Context, source, quarantineRoot string) (Fetched, error) {
	if err := os.MkdirAll(quarantineRoot, 0o700); err != nil {
		return Fetched{}, err
	}
	q, err := os.MkdirTemp(quarantineRoot, "skill-")
	if err != nil {
		return Fetched{}, err
	}
	f := Fetched{Dir: filepath.Join(q, "staging"), Source: source}
	fail := func(err error) (Fetched, error) {
		_ = os.RemoveAll(q)
		return Fetched{}, err
	}
	src := source
	if strings.HasPrefix(source, "https://") {
		url, commit, ok := strings.Cut(strings.TrimPrefix(source, "https://"), "@")
		if !ok || !commitRE.MatchString(commit) {
			return fail(errors.New("a git source must end in @<full 40-character commit>"))
		}
		clone := filepath.Join(q, "clone")
		if err := gitFetch(ctx, "https://"+url, commit, clone); err != nil {
			return fail(err)
		}
		src = clone
	} else if strings.Contains(source, "://") {
		return fail(errors.New("only local folders and https git URLs are supported"))
	}
	if err := copyTree(src, f.Dir); err != nil {
		return fail(err)
	}
	doc, err := readSkillFile(filepath.Join(f.Dir, "SKILL.md"))
	if err != nil {
		return fail(fmt.Errorf("no readable SKILL.md at the top of the source: %w", err))
	}
	// The spec requires the folder to be named after the skill: take the
	// name from the frontmatter, check it, and rename the staging folder.
	front, _, err := splitFrontmatter(string(doc))
	if err != nil {
		return fail(err)
	}
	fields, _, err := parseFrontmatter(front)
	if err != nil {
		return fail(fmt.Errorf("frontmatter: %w", err))
	}
	name := fields["name"]
	if !nameRE.MatchString(name) || len(name) > maxNameLen {
		return fail(fmt.Errorf("invalid skill name %q", name))
	}
	named := filepath.Join(q, name)
	if err := os.Rename(f.Dir, named); err != nil {
		return fail(err)
	}
	f.Dir = named
	s, err := Parse(doc, f.Dir, "project")
	if err != nil {
		return fail(err)
	}
	f.Skill = s
	f.TreeHash, f.Files, err = TreeHash(f.Dir)
	if err != nil {
		return fail(err)
	}
	return f, nil
}

// gitFetch fetches exactly one commit. Only https, no submodules, no hooks,
// no credential prompts.
func gitFetch(ctx context.Context, url, commit, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	run := func(args ...string) error {
		base := []string{"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "core.hooksPath=" + os.DevNull, "-c", "submodule.recurse=false"}
		cmd := exec.CommandContext(ctx, "git", append(base, args...)...) //nolint:gosec // fixed git subcommands; url and commit validated by the caller
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=echo")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := run("init", "-q", dir); err != nil {
		return err
	}
	if err := run("-C", dir, "fetch", "-q", "--depth", "1", url, commit); err != nil {
		return err
	}
	if err := run("-C", dir, "checkout", "-q", "FETCH_HEAD"); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(dir, ".git"))
}

// copyTree copies regular files and folders from src to dst. Symlinks and
// other special files are refused (they could point outside the skill), as
// is anything over the install limits. A .git folder is skipped.
func copyTree(src, dst string) error {
	files, total := 0, int64(0)
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink; skills must be plain files", filepath.ToSlash(rel))
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", filepath.ToSlash(rel))
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files++
		total += info.Size()
		if files > maxInstallFiles || total > maxInstallBytes {
			return fmt.Errorf("skill is too large (limit %d files, %d MB)", maxInstallFiles, maxInstallBytes>>20)
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // walking the source tree
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // inside the quarantine folder
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// TreeHash hashes every file in dir: SHA-256 over the sorted lines
// "<path>\x00<sha256 of contents>\n". Any added, removed, renamed or edited
// file changes it.
func TreeHash(dir string) (string, []File, error) {
	var files []File
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		b, err := os.ReadFile(p) //nolint:gosec // walking the skill folder
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		sum := sha256.Sum256(b)
		files = append(files, File{Path: filepath.ToSlash(rel), Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	h := sha256.New()
	for _, f := range files {
		_, _ = fmt.Fprintf(h, "%s\x00%s\n", f.Path, f.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil)), files, nil
}

// LockEntry records one installed skill.
type LockEntry struct {
	Name        string    `json:"name"`
	Dir         string    `json:"dir"`
	Source      string    `json:"source"`
	TreeHash    string    `json:"tree_hash"`
	InstalledAt time.Time `json:"installed_at"`
	InstalledBy string    `json:"installed_by,omitempty"`
	ApprovalID  string    `json:"approval_id,omitempty"`
}

type lockFile struct {
	Skills []LockEntry `json:"skills"`
}

// ReadLock returns the installed skills keyed by their folder.
func ReadLock(path string) (map[string]LockEntry, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator's own config file
	if errors.Is(err, os.ErrNotExist) {
		return map[string]LockEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var lf lockFile
	if err := json.Unmarshal(b, &lf); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := make(map[string]LockEntry, len(lf.Skills))
	for _, e := range lf.Skills {
		out[lockKey(e.Dir)] = e
	}
	return out, nil
}

func lockKey(dir string) string { return filepath.Clean(dir) }

// Install moves an approved, fetched skill to <workspace skills root>/<name>
// and records it in the lock. It refuses to replace an existing folder.
func Install(f Fetched, skillsRoot, lockPath string, entry LockEntry) (LockEntry, error) {
	// Re-hash right before the move: the quarantine copy must still be what
	// the person approved.
	h, _, err := TreeHash(f.Dir)
	if err != nil {
		return LockEntry{}, err
	}
	if h != f.TreeHash {
		return LockEntry{}, errors.New("the fetched skill changed after it was reviewed; install refused")
	}
	target := filepath.Join(skillsRoot, f.Skill.Name)
	if _, err := os.Stat(target); err == nil {
		return LockEntry{}, fmt.Errorf("a skill named %q already exists at %s", f.Skill.Name, target)
	}
	if err := os.MkdirAll(skillsRoot, 0o750); err != nil {
		return LockEntry{}, err
	}
	if err := os.Rename(f.Dir, target); err != nil {
		if err := copyTree(f.Dir, target); err != nil { // across volumes
			return LockEntry{}, err
		}
		_ = os.RemoveAll(f.Dir)
	}
	_ = os.RemoveAll(filepath.Dir(f.Dir))
	entry.Name, entry.Dir, entry.Source, entry.TreeHash = f.Skill.Name, target, f.Source, f.TreeHash
	if entry.InstalledAt.IsZero() {
		entry.InstalledAt = time.Now().UTC()
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	lock, err := ReadLock(lockPath)
	if err != nil {
		return LockEntry{}, err
	}
	lock[lockKey(target)] = entry
	lf := lockFile{}
	for _, e := range lock {
		lf.Skills = append(lf.Skills, e)
	}
	sort.Slice(lf.Skills, func(i, j int) bool { return lf.Skills[i].Dir < lf.Skills[j].Dir })
	b, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return LockEntry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return LockEntry{}, err
	}
	tmp := lockPath + "." + randomSuffix()
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return LockEntry{}, err
	}
	return entry, os.Rename(tmp, lockPath)
}

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Tampered reports, for each skill installed through the lock, whether its
// files no longer match the recorded tree hash. Skills not in the lock (added
// by hand to the repository) are not checked.
func Tampered(found []Skill, lock map[string]LockEntry) map[string]bool {
	out := map[string]bool{}
	for _, s := range found {
		e, ok := lock[lockKey(s.Dir)]
		if !ok {
			continue
		}
		h, _, err := TreeHash(s.Dir)
		if err != nil || h != e.TreeHash {
			out[s.Name] = true
		}
	}
	return out
}
