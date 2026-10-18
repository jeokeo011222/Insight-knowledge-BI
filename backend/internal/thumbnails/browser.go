// Package thumbnails captures headless screenshots of charts and dashboards and
// caches them on disk, replacing superset/utils/screenshots.py.
package thumbnails

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// browserEnv overrides browser discovery.
const browserEnv = "SUPERSET_CHROME_PATH"

// findBrowser returns the first usable Chromium based browser executable.
func findBrowser() (string, error) {
	if p := strings.TrimSpace(os.Getenv(browserEnv)); p != "" {
		if fileExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("browser from %s does not exist: %s", browserEnv, p)
	}

	for _, candidate := range browserCandidates() {
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no Chromium based browser found (set %s to override)", browserEnv)
}

// browserCandidates lists the well known install locations for the platform.
func browserCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		roots := []string{}
		for _, env := range []string{"PROGRAMFILES(X86)", "PROGRAMFILES", "LOCALAPPDATA"} {
			if v := os.Getenv(env); v != "" {
				roots = append(roots, v)
			}
		}
		roots = append(roots,
			`C:\Program Files (x86)`,
			`C:\Program Files`,
		)
		var out []string
		for _, root := range roots {
			out = append(out,
				filepath.Join(root, `Microsoft\Edge\Application\msedge.exe`),
				filepath.Join(root, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(root, `Chromium\Application\chrome.exe`),
			)
		}
		return out
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	default:
		return []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
			"/usr/bin/microsoft-edge",
		}
	}
}

// fileExists reports whether path is an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
