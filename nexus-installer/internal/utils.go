package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
)

// DetectArch returns amd64 or arm64 for naming consistency.
func DetectArch() string {
	arch := runtime.GOARCH
	if arch == "x86_64" {
		return "amd64"
	}
	if arch == "aarch64" {
		return "arm64"
	}
	return arch
}

// DownloadFile downloads a file from URL to dest.
func DownloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// VerifyChecksum validates a file's SHA256 against a line in checksums.txt.
func VerifyChecksum(filePath, checksumsTxtPath string) error {
	// 1. Calculate file hash
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	calculatedHash := hex.EncodeToString(h.Sum(nil))

	// 2. Read checksums file
	data, err := os.ReadFile(checksumsTxtPath)
	if err != nil {
		return err
	}

	fileName := strings.Split(filePath, "/")[len(strings.Split(filePath, "/"))-1]
	lines := strings.Split(string(data), "\n")
	
	for _, line := range lines {
		if strings.Contains(line, fileName) {
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				expectedHash := parts[0]
				if calculatedHash == expectedHash {
					return nil
				}
				return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", fileName, expectedHash, calculatedHash)
			}
		}
	}

	return fmt.Errorf("checksum entry not found for %s", fileName)
}

// RestoreSELinux restores SELinux contexts for the given binaries.
func RestoreSELinux(paths []string) {
	if _, err := RunCommand("command -v restorecon"); err == nil {
		pathStr := ""
		for _, p := range paths {
			pathStr += p + " "
		}
		RunCommand("sudo restorecon -v " + pathStr)
	}
}
