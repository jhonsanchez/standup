// Package gitscan discovers branches in local clones so issues with a branch
// but no PR yet still show git context.
package gitscan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jhonsanchez/standup/internal/data"
)

// Scan lists local and origin branches of every git repo directly under base.
func Scan(base string) []data.BranchRef {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []data.BranchRef
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		if st, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !st.IsDir() {
			continue
		}
		raw, err := exec.Command("git", "-C", dir, "for-each-ref",
			"--format=%(refname:short)", "refs/heads", "refs/remotes/origin").Output()
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, name := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			name = strings.TrimPrefix(name, "origin/")
			if name == "" || name == "HEAD" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, data.BranchRef{Repo: e.Name(), RepoDir: dir, Name: name})
		}
	}
	return out
}

// DefaultBase returns the repo's default remote branch (e.g. origin/develop).
func DefaultBase(dir string) string {
	out, err := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		return "origin/HEAD"
	}
	return strings.TrimSpace(string(out))
}

// StartBranch creates branch off the default base and pushes it — without
// touching the repo's working tree. If the branch already exists on the
// remote, the fetch alone links it (existed=true). A failed push still
// leaves the local branch (mapping works on this machine); pushErr reports it.
func StartBranch(dir, branch string) (existed bool, pushErr, err error) {
	if out, e := exec.Command("git", "-C", dir, "fetch", "origin").CombinedOutput(); e != nil {
		return false, nil, fmt.Errorf("fetch: %s", firstLine(out))
	}
	if exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch).Run() == nil {
		return true, nil, nil
	}
	if exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Run() != nil {
		if out, e := exec.Command("git", "-C", dir, "branch", branch, DefaultBase(dir)).CombinedOutput(); e != nil {
			return false, nil, fmt.Errorf("branch: %s", firstLine(out))
		}
	}
	if out, e := exec.Command("git", "-C", dir, "push", "-u", "origin", branch).CombinedOutput(); e != nil {
		return false, fmt.Errorf("push: %s", firstLine(out)), nil
	}
	return false, nil, nil
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
