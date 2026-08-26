// Package upgrade implements `standup upgrade`: it detects how the binary
// was installed and updates it in place.
package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const repo = "jhonsanchez/standup"

// Run upgrades the running binary. current is the running version ("dev" for
// source builds).
func Run(current string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	// Homebrew-managed installs should stay brew-managed.
	if strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "/linuxbrew/") {
		fmt.Println("installed via Homebrew — run: brew update && brew upgrade standup")
		return nil
	}

	// go install layout: binary in GOBIN/GOPATH/bin and a go toolchain around.
	if goBin, err := exec.LookPath("go"); err == nil && inGoBin(exe) {
		fmt.Println("updating via go install…")
		cmd := exec.Command(goBin, "install", "github.com/"+repo+"@latest")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}

	return downloadLatest(exe, current)
}

func inGoBin(exe string) bool {
	dir := filepath.Dir(exe)
	if gobin := goEnv("GOBIN"); gobin != "" && dir == gobin {
		return true
	}
	if gopath := goEnv("GOPATH"); gopath != "" && dir == filepath.Join(gopath, "bin") {
		return true
	}
	home, _ := os.UserHomeDir()
	return dir == filepath.Join(home, "go", "bin")
}

func goEnv(key string) string {
	out, err := exec.Command("go", "env", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type release struct {
	Tag    string
	assets map[string]string // name → download URL
}

func latestRelease(client *http.Client) (*release, error) {
	resp, err := client.Get("https://api.github.com/repos/" + repo + "/releases/latest")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching latest release: %s", resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	r := &release{Tag: rel.TagName, assets: map[string]string{}}
	for _, a := range rel.Assets {
		r.assets[a.Name] = a.URL
	}
	return r, nil
}

func fetchBytes(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// downloadVerified fetches the release archive for this OS/arch, verifies it
// against checksums.txt, and returns a temp file holding the binary.
func downloadVerified(client *http.Client, rel *release) (string, error) {
	want := fmt.Sprintf("standup_%s_%s_%s.tar.gz",
		strings.TrimPrefix(rel.Tag, "v"), runtime.GOOS, runtime.GOARCH)
	assetURL := rel.assets[want]
	if assetURL == "" {
		return "", fmt.Errorf("no release asset %q in %s", want, rel.Tag)
	}
	archive, err := fetchBytes(client, assetURL)
	if err != nil {
		return "", err
	}

	if sumsURL := rel.assets["checksums.txt"]; sumsURL != "" {
		sums, err := fetchBytes(client, sumsURL)
		if err != nil {
			return "", fmt.Errorf("fetching checksums: %w", err)
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(archive))
		ok := false
		for _, line := range strings.Split(string(sums), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == want {
				if fields[0] != sum {
					return "", fmt.Errorf("checksum mismatch for %s", want)
				}
				ok = true
			}
		}
		if !ok {
			return "", fmt.Errorf("%s not listed in checksums.txt", want)
		}
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("binary not found in archive")
		}
		if err != nil {
			return "", err
		}
		if filepath.Base(hdr.Name) != "standup" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		tmp := filepath.Join(os.TempDir(), "standup-upgrade")
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			os.Remove(tmp)
			return "", err
		}
		f.Close()
		return tmp, nil
	}
}

// downloadLatest replaces exe with the latest release binary for this OS/arch.
func downloadLatest(exe, current string) error {
	if runtime.GOOS == "windows" {
		fmt.Println("on Windows, download the latest release manually:")
		fmt.Println("  https://github.com/" + repo + "/releases/latest")
		return nil
	}
	client := &http.Client{Timeout: 60 * time.Second}
	rel, err := latestRelease(client)
	if err != nil {
		return err
	}
	if rel.Tag == "v"+current {
		fmt.Println("already up to date (" + rel.Tag + ")")
		return nil
	}
	fmt.Printf("downloading standup %s (%s/%s, sha256-verified)…\n", rel.Tag, runtime.GOOS, runtime.GOARCH)
	tmp, err := downloadVerified(client, rel)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := installBinary(tmp, exe); err != nil {
		return err
	}
	fmt.Println("✓ updated to " + rel.Tag + " (" + exe + ")")
	return nil
}

// AutoUpdate silently updates the install when a newer release exists.
// skipReason explains a safe no-op (go-managed, unwritable dir, …).
func AutoUpdate(current string) (tag, skipReason string, err error) {
	if current == "" || current == "dev" || runtime.GOOS == "windows" {
		return "", "", nil
	}
	exe, e := os.Executable()
	if e != nil {
		return "", "", nil
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "/linuxbrew/") {
		t, err := brewAutoUpdate(current)
		return t, "", err
	}
	if inGoBin(exe) {
		return "", "go-managed install — run: go install github.com/jhonsanchez/standup@latest", nil
	}
	// Only proceed when the target directory is writable — never escalate.
	if f, e := os.OpenFile(exe+".new", os.O_CREATE|os.O_WRONLY, 0o755); e != nil {
		return "", exe + " is not user-writable — run `standup upgrade` (sudo) or reinstall via install.sh to ~/.local/bin", nil
	} else {
		f.Close()
		os.Remove(exe + ".new")
	}

	client := &http.Client{Timeout: 120 * time.Second}
	rel, e2 := latestRelease(client)
	if e2 != nil {
		return "", "", e2
	}
	if rel.Tag == "v"+current {
		return "", "", nil
	}
	tmp, e3 := downloadVerified(client, rel)
	if e3 != nil {
		return "", "", e3
	}
	defer os.Remove(tmp)
	data, e4 := os.ReadFile(tmp)
	if e4 != nil {
		return "", "", e4
	}
	staged := exe + ".new"
	if e5 := os.WriteFile(staged, data, 0o755); e5 != nil {
		return "", "", e5
	}
	if e6 := os.Rename(staged, exe); e6 != nil {
		os.Remove(staged)
		return "", "", e6
	}
	return rel.Tag, "", nil
}

// installBinary replaces exe with src, escalating with sudo when the target
// directory isn't writable (e.g. /usr/local/bin).
func installBinary(src, exe string) error {
	// Atomic same-directory swap when we have write access.
	staged := exe + ".new"
	if data, err := os.ReadFile(src); err == nil {
		if err := os.WriteFile(staged, data, 0o755); err == nil {
			if err := os.Rename(staged, exe); err == nil {
				return nil
			}
			os.Remove(staged)
		} else if !os.IsPermission(err) {
			return err
		}
	}
	// No write access: fall back to sudo install.
	fmt.Printf("%s is not writable — escalating with sudo…\n", filepath.Dir(exe))
	cmd := exec.Command("sudo", "install", "-m", "755", src, exe)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo install failed: %w — run manually: sudo install -m 755 %s %s", err, src, exe)
	}
	return nil
}

// InstallKind reports how the running binary is installed.
func InstallKind() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	switch {
	case strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "/linuxbrew/"):
		return "brew"
	case inGoBin(exe):
		return "go install"
	default:
		return "binary"
	}
}

// brewAutoUpdate keeps brew installs current the brew way: refresh the tap
// clone (it goes stale) and run brew upgrade in the background.
func brewAutoUpdate(current string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	rel, err := latestRelease(client)
	if err != nil || rel.Tag == "v"+current {
		return "", err
	}
	if tap, err := exec.Command("brew", "--repository", "jhonsanchez/tap").Output(); err == nil {
		_ = exec.Command("git", "-C", strings.TrimSpace(string(tap)), "pull", "--quiet").Run()
	}
	out, err := exec.Command("brew", "upgrade", "standup").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("brew upgrade: %s", strings.Join(strings.Fields(string(out)), " ")[:min(120, len(out))])
	}
	if strings.Contains(string(out), "Upgraded") {
		return rel.Tag, nil
	}
	return "", nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
