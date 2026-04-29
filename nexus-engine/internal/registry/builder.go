// Package registry handles container image building and pushing via nerdctl.
// This is the ONLY place in nexus-engine that shells out to an external process.
package registry

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nexus-oss/nexus/nexus-engine/internal/config"
)

// Builder handles nerdctl build + push operations.
type Builder struct {
	cfg config.RegistryConfig
}

// NewBuilder creates a Builder from registry config.
func NewBuilder(cfg config.RegistryConfig) *Builder {
	return &Builder{cfg: cfg}
}

// BuildResult is returned after a successful build.
type BuildResult struct {
	Image     string        // full image reference e.g. localhost:5000/pwn-101:latest
	Tag       string        // just the tag
	Ports     []int         // ports extracted from EXPOSE
	Duration  time.Duration
	BuildLog  string
}

// Build runs nerdctl build and pushes the resulting image to the configured registry.
// dockerfilePath must be an absolute or relative path to a Dockerfile on the engine host.
// challengeName is used as the image name (e.g. "pwn-101").
func (b *Builder) Build(challengeName, dockerfilePath string) (*BuildResult, error) {
	start := time.Now()

	// Validate the Dockerfile exists.
	if err := validateDockerfile(dockerfilePath); err != nil {
		return nil, fmt.Errorf("dockerfile validation: %w", err)
	}

	// Extract ports from EXPOSE instructions.
	exposedPorts := parseExposedPorts(dockerfilePath)

	// Resolve build context directory (directory containing Dockerfile).
	buildContext := filepath.Dir(dockerfilePath)

	tag := "latest"
	imageRef := fmt.Sprintf("%s/%s:%s", b.cfg.URL, sanitizeImageName(challengeName), tag)

	// nerdctl build -t <image> <context>
	buildArgs := []string{
		"build",
		"--namespace", "k8s.io", // Build into containerd k8s.io namespace for k3s
		"-t", imageRef,
		"-f", dockerfilePath,
		buildContext,
	}

	var buildOut bytes.Buffer
	buildCmd := exec.Command("nerdctl", buildArgs...)
	buildCmd.Stdout = &buildOut
	buildCmd.Stderr = &buildOut

	if err := buildCmd.Run(); err != nil {
		return nil, fmt.Errorf("nerdctl build failed: %w\noutput: %s", err, buildOut.String())
	}

	// nerdctl push <image>
	var pushOut bytes.Buffer
	pushArgs := []string{"push", "--namespace", "k8s.io"}
	if authArgs := b.authArgs(); len(authArgs) > 0 {
		pushArgs = append(pushArgs, authArgs...)
	}
	pushArgs = append(pushArgs, imageRef)

	pushCmd := exec.Command("nerdctl", pushArgs...)
	pushCmd.Stdout = &pushOut
	pushCmd.Stderr = &pushOut

	if err := pushCmd.Run(); err != nil {
		return nil, fmt.Errorf("nerdctl push failed: %w\noutput: %s", err, pushOut.String())
	}

	buildLog := buildOut.String() + "\n" + pushOut.String()

	return &BuildResult{
		Image:    imageRef,
		Tag:      tag,
		Ports:    exposedPorts,
		Duration: time.Since(start),
		BuildLog: buildLog,
	}, nil
}

// parseExposedPorts scans a Dockerfile for EXPOSE instructions.
func parseExposedPorts(path string) []int {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var ports []int
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "EXPOSE") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			// Handle "EXPOSE 80 443" or "EXPOSE 8080/tcp"
			for i := 1; i < len(fields); i++ {
				pStr := fields[i]
				if idx := strings.Index(pStr, "/"); idx != -1 {
					pStr = pStr[:idx]
				}
				if p, err := strconv.Atoi(pStr); err == nil {
					ports = append(ports, p)
				}
			}
		}
	}
	return ports
}

// authArgs returns extra nerdctl flags for registry authentication.
func (b *Builder) authArgs() []string {
	switch b.cfg.AuthType {
	case "basic", "ghcr":
		// For push, nerdctl reads from ~/.docker/config.json which setup.sh writes.
		return nil
	case "awsecr":
		// ECR token refresh is handled by setup.sh's cron job writing to containerd config.
		return nil
	default:
		return nil
	}
}

// validateDockerfile checks that the file exists and is readable.
func validateDockerfile(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path %q: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("dockerfile not found: %s", abs)
		}
		return fmt.Errorf("cannot access dockerfile %s: %w", abs, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, not a Dockerfile", abs)
	}
	return nil
}

// sanitizeImageName converts challenge names to valid OCI image name components.
// Replaces spaces and underscores with hyphens, lowercases.
func sanitizeImageName(name string) string {
	r := strings.ToLower(name)
	r = strings.ReplaceAll(r, " ", "-")
	r = strings.ReplaceAll(r, "_", "-")
	var b strings.Builder
	for _, c := range r {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' {
			b.WriteRune(c)
		}
	}
	return strings.Trim(b.String(), "-.")
}
