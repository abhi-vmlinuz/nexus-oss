package registry

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
	"gopkg.in/yaml.v3"
)

// composeFile represents a minimal docker-compose.yml structure.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Build struct {
		Context    string `yaml:"context"`
		Dockerfile string `yaml:"dockerfile"`
	} `yaml:"build"`
	Image   string   `yaml:"image"`
	Ports   []string `yaml:"ports"`
	Expose  []string `yaml:"expose"`
	EnvFile []string `yaml:"env_file"`
	Environment yaml.Node `yaml:"environment"`
}

// ParseComposeResult is returned after a successful compose parse + build.
type ParseComposeResult struct {
	Containers []state.ContainerSpec
	AllPorts   []int
}

// ParseAndBuild reads a docker-compose.yml, builds any local service images
// via nerdctl (engine runs as root), pulls public images, and returns the
// resulting container specs ready for pod registration.
func (b *Builder) ParseAndBuild(challengeName, composePath string) (*ParseComposeResult, error) {
	data, err := os.ReadFile(composePath)
	if err != nil {
		return nil, fmt.Errorf("cannot read compose file: %w", err)
	}

	// Expand env vars in the compose file (e.g. ${REGISTRY_URL}).
	expanded := os.ExpandEnv(string(data))

	var cf composeFile
	if err := yaml.Unmarshal([]byte(expanded), &cf); err != nil {
		return nil, fmt.Errorf("invalid compose yaml: %w", err)
	}
	if len(cf.Services) == 0 {
		return nil, fmt.Errorf("no services defined in compose file %s", composePath)
	}

	composeDir := filepath.Dir(composePath)
	result := &ParseComposeResult{}
	portSeen := map[int]bool{}

	for svcName, svc := range cf.Services {
		var imageRef string

		if svc.Build.Context != "" {
			// Local service: build with nerdctl. Engine runs as root, so no sudo needed.
			context := svc.Build.Context
			if !filepath.IsAbs(context) {
				context = filepath.Join(composeDir, context)
			}
			dockerfile := svc.Build.Dockerfile
			if dockerfile != "" && !filepath.IsAbs(dockerfile) {
				dockerfile = filepath.Join(context, dockerfile)
			}
			imageRef = fmt.Sprintf("%s/%s-%s:latest", b.cfg.URL, sanitizeImageName(challengeName), sanitizeImageName(svcName))

			if err := b.buildAndPush(imageRef, context, dockerfile); err != nil {
				return nil, fmt.Errorf("service %q: %w", svcName, err)
			}
		} else if svc.Image != "" {
			// Public image: pull into k8s.io namespace.
			imageRef = svc.Image
			if err := b.pull(imageRef); err != nil {
				return nil, fmt.Errorf("service %q: %w", svcName, err)
			}
		} else {
			return nil, fmt.Errorf("service %q: must have either build or image", svcName)
		}

		// Parse ports from both "ports" and "expose" keys.
		ports, err := parseComposePorts(svc.Ports)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", svcName, err)
		}
		for _, p := range ports {
			if !portSeen[p] {
				portSeen[p] = true
				result.AllPorts = append(result.AllPorts, p)
			}
		}

		result.Containers = append(result.Containers, state.ContainerSpec{
			Name:  svcName,
			Image: imageRef,
			Ports: ports,
		})
	}

	return result, nil
}

// buildAndPush runs nerdctl build + push. Engine runs as root via systemd.
func (b *Builder) buildAndPush(imageRef, context, dockerfile string) error {
	args := []string{
		"build",
		"--namespace", "k8s.io",
		"-t", imageRef,
	}
	if dockerfile != "" {
		args = append(args, "-f", dockerfile)
	}
	args = append(args, context)

	var out bytes.Buffer
	cmd := exec.Command("nerdctl", args...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nerdctl build failed: %w\noutput: %s", err, out.String())
	}

	out.Reset()
	pushArgs := []string{"push", "--namespace", "k8s.io"}
	if auth := b.authArgs(); len(auth) > 0 {
		pushArgs = append(pushArgs, auth...)
	}
	pushArgs = append(pushArgs, imageRef)

	pushCmd := exec.Command("nerdctl", pushArgs...)
	pushCmd.Stdout = &out
	pushCmd.Stderr = &out
	if err := pushCmd.Run(); err != nil {
		return fmt.Errorf("nerdctl push failed: %w\noutput: %s", err, out.String())
	}
	return nil
}

// pull pre-pulls a public image into the k8s.io namespace.
func (b *Builder) pull(imageRef string) error {
	var out bytes.Buffer
	cmd := exec.Command("nerdctl", "pull", "--namespace", "k8s.io", imageRef)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nerdctl pull %q failed: %w\noutput: %s", imageRef, err, out.String())
	}
	return nil
}

// parseComposePorts parses port mappings like "8080:80" or "443" into ints.
// We always extract the container-side (right) port.
func parseComposePorts(raw []string) ([]int, error) {
	var ports []int
	for _, r := range raw {
		parts := strings.Split(r, ":")
		pStr := strings.Split(parts[len(parts)-1], "/")[0] // strip /tcp etc.
		var p int
		if _, err := fmt.Sscanf(pStr, "%d", &p); err != nil {
			return nil, fmt.Errorf("invalid port %q in compose file", r)
		}
		ports = append(ports, p)
	}
	return ports, nil
}
