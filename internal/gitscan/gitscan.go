// Package gitscan discovers branches in local clones so issues with a branch
// but no PR yet still show git context.
package gitscan

import (
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
