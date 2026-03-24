package versioncheck

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dirName        = ".cllmhub"
	cacheFile      = "version-check.json"
	checkInterval  = 24 * time.Hour
	repo           = "cllmhub/cllmhub-cli"
	requestTimeout = 5 * time.Second
)

type cache struct {
	LatestVersion string    `json:"latest_version"`
	CheckedAt     time.Time `json:"checked_at"`
}

// Result holds the outcome of a background version check.
type Result struct {
	Available      bool
	LatestVersion  string
	CurrentVersion string
}

// Checker performs a background version check against GitHub releases.
type Checker struct {
	currentVersion string
	mu             sync.Mutex
	result         *Result
	done           chan struct{}
}

// New creates a Checker and starts the background check immediately.
func New(currentVersion string) *Checker {
	c := &Checker{
		currentVersion: currentVersion,
		done:           make(chan struct{}),
	}
	go c.run()
	return c
}

// Result returns the check result. It blocks briefly (up to the HTTP timeout)
// waiting for the background goroutine to finish. Returns nil if no update is
// available or the check could not be completed.
func (c *Checker) Result() *Result {
	select {
	case <-c.done:
	case <-time.After(requestTimeout):
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.result
}

func (c *Checker) run() {
	defer close(c.done)

	cached, err := loadCache()
	if err == nil && time.Since(cached.CheckedAt) < checkInterval {
		c.setResult(cached.LatestVersion)
		return
	}

	latest, err := fetchLatestVersion()
	if err != nil {
		return
	}

	_ = saveCache(cache{LatestVersion: latest, CheckedAt: time.Now()})
	c.setResult(latest)
}

func (c *Checker) setResult(latest string) {
	current := normalizeVersion(c.currentVersion)
	latest = normalizeVersion(latest)

	if current == "" || latest == "" || current == "dev" {
		return
	}
	if !isNewer(latest, current) {
		return
	}

	c.mu.Lock()
	c.result = &Result{
		Available:      true,
		LatestVersion:  latest,
		CurrentVersion: current,
	}
	c.mu.Unlock()
}

// isNewer returns true if version a is strictly newer than version b.
// Compares semver-style dot-separated integers (e.g. "0.5.3" > "0.5.2").
func isNewer(a, b string) bool {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		ai, bi := 0, 0
		if i < len(ap) {
			ai, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			bi, _ = strconv.Atoi(bp[i])
		}
		if ai > bi {
			return true
		}
		if ai < bi {
			return false
		}
	}
	return false
}

func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

func cachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, dirName, cacheFile), nil
}

func loadCache() (*cache, error) {
	p, err := cachePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func saveCache(c cache) error {
	p, err := cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

func fetchLatestVersion() (string, error) {
	client := &http.Client{Timeout: requestTimeout}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}
