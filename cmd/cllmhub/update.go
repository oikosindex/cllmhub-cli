package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update cllmhub to the latest version",
	Long:  `Update the CLI binary to the latest release. Uses go install if Go is available, otherwise downloads a pre-built binary.`,
	Example: `  cllmhub update`,
	RunE:  runUpdate,
}

const (
	repo      = "oikosindex/cllmhub-cli"
	binaryName = "cllmhub"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
}

func runUpdate(cmd *cobra.Command, args []string) error {
	// Try go install first
	if goPath, err := exec.LookPath("go"); err == nil {
		fmt.Println("Go found, updating via go install...")
		install := exec.Command(goPath, "install", fmt.Sprintf("github.com/%s/cmd/%s@latest", repo, binaryName))
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			return fmt.Errorf("go install failed: %w", err)
		}
		fmt.Println("Updated successfully.")
		return nil
	}

	// Fall back to downloading pre-built binary
	fmt.Println("Go not found, downloading pre-built binary...")

	version, err := getLatestVersion()
	if err != nil {
		return fmt.Errorf("failed to get latest version: %w", err)
	}
	fmt.Printf("Latest version: %s\n", version)

	filename := fmt.Sprintf("%s-%s-%s", binaryName, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}

	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, version, filename)
	fmt.Printf("Downloading %s...\n", url)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// Write to a temp file next to the current binary
	currentBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine current binary path: %w", err)
	}

	tmpFile, err := os.CreateTemp("", binaryName+"-update-*")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download write failed: %w", err)
	}
	tmpFile.Close()

	// Verify checksum
	fmt.Println("Verifying checksum...")
	if err := verifyChecksum(version, filename, tmpFile.Name()); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	// Replace the current binary
	if err := os.Rename(tmpFile.Name(), currentBin); err != nil {
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	fmt.Printf("Updated to %s successfully.\n", version)
	return nil
}

func getLatestVersion() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
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

	if release.TagName == "" {
		return "", fmt.Errorf("no release found")
	}

	return release.TagName, nil
}

// verifyChecksum downloads checksums.txt from the release and verifies the
// SHA-256 of the downloaded file matches the expected value.
func verifyChecksum(version, filename, filepath string) error {
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/checksums.txt", repo, version)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checksums not available (HTTP %d); skipping verification", resp.StatusCode)
	}

	// Parse checksums.txt (format: "<hash>  <filename>" per line)
	var expected string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 2 && parts[1] == filename {
			expected = parts[0]
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("no checksum found for %s", filename)
	}

	// Compute actual SHA-256
	f, err := os.Open(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))

	if actual != expected {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
	}

	fmt.Println("Checksum verified.")
	return nil
}
