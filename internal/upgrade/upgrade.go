// Package upgrade implements `standup upgrade`: it detects how the binary
// was installed and updates it in place.
package upgrade

import (
	"archive/tar"
	"compress/gzip"
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

// downloadLatest replaces exe with the latest release binary for this OS/arch.
func downloadLatest(exe, current string) error {
	if runtime.GOOS == "windows" {
		fmt.Println("on Windows, download the latest release manually:")
		fmt.Println("  https://github.com/" + repo + "/releases/latest")
		return nil
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/" + repo + "/releases/latest")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching latest release: %s", resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return err
	}
	if rel.TagName == "v"+current {
		fmt.Println("already up to date (" + rel.TagName + ")")
		return nil
	}

	want := fmt.Sprintf("standup_%s_%s_%s.tar.gz",
		strings.TrimPrefix(rel.TagName, "v"), runtime.GOOS, runtime.GOARCH)
	assetURL := ""
	for _, a := range rel.Assets {
		if a.Name == want {
			assetURL = a.URL
			break
		}
	}
	if assetURL == "" {
		return fmt.Errorf("no release asset %q in %s", want, rel.TagName)
	}

	fmt.Printf("downloading %s %s…\n", want, rel.TagName)
	dl, err := client.Get(assetURL)
	if err != nil {
		return err
	}
	defer dl.Body.Close()
	if dl.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading asset: %s", dl.Status)
	}

	gz, err := gzip.NewReader(dl.Body)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("binary not found in archive")
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) != "standup" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		tmp := filepath.Join(os.TempDir(), "standup-upgrade")
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		f.Close()
		defer os.Remove(tmp)
		if err := installBinary(tmp, exe); err != nil {
			return err
		}
		fmt.Println("✓ updated to " + rel.TagName + " (" + exe + ")")
		return nil
	}
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
