package internal

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Mode          string
	RedisBackend  string
	RegistryType  string
	RegistryURL   string
	RegistryUser  string
	RegistryPass  string
	EnginePort    string
	AgentPort     string
	K8sNamespace  string
	RedisURL      string
	NodeAgentAddr string
}

// detectPkgManager mimics the logic in setup.sh
func detectPkgManager() string {
	if _, err := RunCommand("command -v apt-get"); err == nil {
		return "apt"
	}
	if _, err := RunCommand("command -v dnf"); err == nil {
		return "dnf"
	}
	if _, err := RunCommand("command -v yum"); err == nil {
		return "yum"
	}
	if _, err := RunCommand("command -v pacman"); err == nil {
		return "pacman"
	}
	if _, err := RunCommand("command -v zypper"); err == nil {
		return "zypper"
	}
	return "unknown"
}

// InitializeInstaller handles Phase 0: sudo check and log init
func InitializeInstaller() (string, error) {
	if _, err := RunCommand("sudo -v"); err != nil {
		return "", fmt.Errorf("sudo privileges required: %w", err)
	}
	// Init log file
	_, err := RunCommand("sudo touch /var/log/nexus-install.log && sudo chmod 666 /var/log/nexus-install.log")
	if err != nil {
		return "", fmt.Errorf("failed to init log file: %w", err)
	}
	return "✓ Environment initialized", nil
}

// resolvePkg maps logical names to distro-specific packages
func resolvePkg(mgr, pkg string) string {
	switch mgr {
	case "apt":
		switch pkg {
		case "redis":
			return "redis-server"
		case "wireguard":
			return "wireguard wireguard-tools"
		case "ca-certs":
			return "ca-certificates"
		case "golang":
			return "golang-go"
		}
	case "dnf", "yum":
		switch pkg {
		case "ca-certs":
			return "ca-certificates"
		case "wireguard":
			return "wireguard-tools"
		case "build-essential":
			return "development-tools" // logical group
		}
	}
	return pkg
}

// InstallPackages handles Phase 1 with distro detection
func InstallPackages(backend string) (string, error) {
	mgr := detectPkgManager()
	if mgr == "unknown" {
		return "", fmt.Errorf("unsupported package manager")
	}

	logicalPkgs := []string{"curl", "wget", "jq", "git", "ca-certs", "iptables", "ipset", "wireguard", "golang", "rust", "cargo"}
	if backend == "host" {
		logicalPkgs = append(logicalPkgs, "redis")
	}

	var resolved []string
	for _, p := range logicalPkgs {
		resolved = append(resolved, resolvePkg(mgr, p))
	}

	pkgStr := ""
	for _, p := range resolved {
		pkgStr += p + " "
	}

	var cmd string
	switch mgr {
	case "apt":
		cmd = fmt.Sprintf("sudo apt-get update -y && sudo apt-get install -y %s", pkgStr)
	case "dnf":
		cmd = fmt.Sprintf("sudo dnf install -y %s", pkgStr)
	case "yum":
		cmd = fmt.Sprintf("sudo yum install -y %s", pkgStr)
	case "pacman":
		cmd = fmt.Sprintf("sudo pacman -S --noconfirm --needed %s", pkgStr)
	case "zypper":
		cmd = fmt.Sprintf("sudo zypper install -y %s", pkgStr)
	}

	return RunCommand(cmd)
}

// InstallK3s handles Phase 2
func InstallK3s(namespace string) (string, error) {
	out := ""
	if _, err := RunCommand("command -v k3s"); err != nil {
		o, err := RunCommand("curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC=\"--disable=traefik\" sh -")
		if err != nil {
			return o, err
		}
		out += o
	}
	o, err := RunCommand(fmt.Sprintf("sudo k3s kubectl create namespace %s --dry-run=client -o yaml | sudo k3s kubectl apply -f -", namespace))
	return out + o, err
}

// InstallNerdctl handles Phase 3
func InstallNerdctl(user string) (string, error) {
	out := ""
	if _, err := RunCommand("command -v nerdctl"); err != nil {
		arch := "amd64"
		cmd := fmt.Sprintf(`NV="1.7.6"; NA="%s"; TMPDIR=$(mktemp -d); 
		curl -fsSL "https://github.com/containerd/nerdctl/releases/download/v${NV}/nerdctl-full-${NV}-linux-${NA}.tar.gz" -o "$TMPDIR/nerdctl.tar.gz";
		sudo tar -xzf "$TMPDIR/nerdctl.tar.gz" -C /usr/local;
		rm -rf "$TMPDIR"`, arch)
		o, err := RunCommand(cmd)
		if err != nil {
			return o, err
		}
		out += o
	}
	RunCommand(fmt.Sprintf("sudo groupadd -f nexus && sudo usermod -aG nexus %s", user))
	o, err := RunCommand("sudo chown root:nexus /run/k3s/containerd/containerd.sock && sudo chmod 660 /run/k3s/containerd/containerd.sock")
	return out + o, err
}

// SetupRegistry handles Phase 4
func SetupRegistry(regType, regURL, user, pass string) (string, error) {
	switch regType {
	case "local":
		if _, err := RunCommand("sudo nerdctl ps -a | grep nexus-registry"); err == nil {
			return "Registry already exists, skipping...", nil
		}
		return RunCommand("sudo nerdctl run -d --name nexus-registry --restart always -p 5000:5000 registry:2")
	case "dockerhub", "ghcr":
		if user != "" && pass != "" {
			host := "docker.io"
			if regType == "ghcr" {
				host = "ghcr.io"
			}
			return RunCommand(fmt.Sprintf("echo %s | sudo nerdctl login %s -u %s --password-stdin", pass, host, user))
		}
	}
	return "No registry auth required", nil
}

// InstallRedis handles Phase 5
func InstallRedis(backend, url string) (string, error) {
	if backend == "docker" {
		if _, err := RunCommand("sudo nerdctl ps -a | grep nexus-redis"); err == nil {
			return "Redis already exists, skipping...", nil
		}
		return RunCommand("sudo nerdctl run -d --name nexus-redis --restart always -p 6379:6379 redis:7-alpine")
	}
	svc := "redis"
	if _, err := RunCommand("systemctl list-unit-files redis-server.service"); err == nil {
		svc = "redis-server"
	}
	return RunCommand(fmt.Sprintf("sudo systemctl enable %s && sudo systemctl start %s", svc, svc))
}

// SetupWireGuard handles Phase 6
func SetupWireGuard() (string, error) {
	if _, err := os.Stat("/etc/wireguard/wg0.conf"); err == nil {
		return "WireGuard config already exists", nil
	}
	cmd := `sudo mkdir -p /etc/wireguard && sudo chmod 700 /etc/wireguard;
	WG_KEY=$(wg genkey);
	WG_PUB=$(echo "$WG_KEY" | wg pubkey);
	echo "[Interface]
Address = 10.8.0.1/24
ListenPort = 51820
PrivateKey = $WG_KEY
PostUp = iptables -A FORWARD -i wg0 -j ACCEPT; iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
PostDown = iptables -D FORWARD -i wg0 -j ACCEPT; iptables -t nat -D POSTROUTING -o eth0 -j MASQUERADE" | sudo tee /etc/wireguard/wg0.conf > /dev/null;
	sudo chmod 600 /etc/wireguard/wg0.conf;
	sudo systemctl enable wg-quick@wg0 && sudo systemctl start wg-quick@wg0;
	echo $WG_PUB`
	return RunCommand(cmd)
}

// BuildAndInstallBinaries handles Phase 7 (The missing piece!)
func BuildAndInstallBinaries(repoRoot string) (string, error) {
	out := ""
	
	// Paths
	enginePath := filepath.Join(repoRoot, "nexus-engine")
	cliPath := filepath.Join(repoRoot, "nexus-cli")
	agentPath := filepath.Join(repoRoot, "nexus-node-agent")

	// Build Engine (main is in cmd/)
	o1, err := RunCommand(fmt.Sprintf("cd %s && go build -o /tmp/nexus-engine ./cmd && sudo mv /tmp/nexus-engine /usr/local/bin/", enginePath))
	if err != nil {
		return o1, fmt.Errorf("engine build failed: %w", err)
	}
	out += "✓ Nexus Engine installed\n"

	// Build CLI (main is in root)
	o2, err := RunCommand(fmt.Sprintf("cd %s && go build -o /tmp/nexus . && sudo mv /tmp/nexus /usr/local/bin/", cliPath))
	if err != nil {
		return o2, fmt.Errorf("cli build failed: %w", err)
	}
	out += "✓ Nexus CLI installed\n"

	// Build Agent (Rust)
	o3, err := RunCommand(fmt.Sprintf("cd %s && cargo build --release && sudo mv target/release/nexus-node-agent /usr/local/bin/", agentPath))
	if err != nil {
		return o3, fmt.Errorf("agent build failed: %w", err)
	}
	out += "✓ Nexus Node Agent installed\n"

	// Restore SELinux contexts (Critical for Fedora/RHEL)
	RunCommand("sudo restorecon -v /usr/local/bin/nexus /usr/local/bin/nexus-engine /usr/local/bin/nexus-node-agent")

	return out, nil
}

// WriteConfigFile handles the Nexus config JSON
func WriteConfigFile(home string, conf Config) (string, error) {
	dir := filepath.Join(home, ".config", "nexus")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}

	content := fmt.Sprintf(`{
  "engine": {
    "url": "http://localhost:%s",
    "mode": "%s"
  },
  "registry": {
    "type": "%s",
    "url": "%s",
    "auth": {
      "type": "none",
      "username": "%s",
      "password": "%s"
    }
  },
  "redis": {
    "url": "%s"
  },
  "node_agent": {
    "addr": "%s"
  },
  "k8s": {
    "namespace": "%s"
  }
}`, conf.EnginePort, conf.Mode, conf.RegistryType, conf.RegistryURL, conf.RegistryUser, conf.RegistryPass, conf.RedisURL, conf.NodeAgentAddr, conf.K8sNamespace)

	err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0600)
	if err != nil {
		return "", err
	}
	return "Configuration written to " + filepath.Join(dir, "config.json"), nil
}

// SetupServices handles Phase 8
func SetupServices(mode, port, redisURL, regURL, agentAddr, namespace string) (string, error) {
	agentSvc := fmt.Sprintf(`[Unit]
Description=Nexus OSS Node Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/nexus-node-agent
Restart=on-failure
Environment=NEXUS_MODE=%s
Environment=NODE_AGENT_LISTEN_ADDR=0.0.0.0:50051
Environment=NODE_AGENT_INSECURE=true
AmbientCapabilities=CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_ADMIN

[Install]
WantedBy=multi-user.target`, mode)

	engineSvc := fmt.Sprintf(`[Unit]
Description=Nexus OSS Engine
After=network.target redis.service nexus-node-agent.service

[Service]
Type=simple
ExecStart=/usr/local/bin/nexus-engine
Restart=on-failure
Environment=NEXUS_MODE=%s
Environment=NEXUS_PORT=%s
Environment=NEXUS_REDIS_URL=%s
Environment=NEXUS_REGISTRY_URL=%s
Environment=NEXUS_NODE_AGENT_ADDR=%s
Environment=NEXUS_K3S_NAMESPACE=%s
Environment=KUBECONFIG=/etc/rancher/k3s/k3s.yaml

[Install]
WantedBy=multi-user.target`, mode, port, redisURL, regURL, agentAddr, namespace)

	os.WriteFile("/tmp/nexus-node-agent.service", []byte(agentSvc), 0644)
	os.WriteFile("/tmp/nexus-engine.service", []byte(engineSvc), 0644)

	RunCommand("sudo mv /tmp/nexus-node-agent.service /etc/systemd/system/")
	RunCommand("sudo mv /tmp/nexus-engine.service /etc/systemd/system/")
	RunCommand("sudo restorecon -v /etc/systemd/system/nexus-*.service")
	RunCommand("sudo systemctl daemon-reload")
	RunCommand("sudo systemctl enable nexus-node-agent nexus-engine")
	return RunCommand("sudo systemctl restart nexus-node-agent nexus-engine")
}
