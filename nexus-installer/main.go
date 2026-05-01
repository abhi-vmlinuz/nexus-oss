package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nexus-oss/nexus/nexus-installer/internal"
	"strings"
)

type logMsg string

type logWriter struct {
	program *tea.Program
}

func (lw logWriter) Write(p []byte) (n int, err error) {
	s := internal.StripANSI(string(p))
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			lw.program.Send(logMsg(line))
		}
	}
	return len(p), nil
}

func main() {
	m := NewModel()
	p := tea.NewProgram(m, tea.WithAltScreen())
	internal.GlobalOutputWriter = logWriter{program: p}

	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}
}

func (m Model) Init() tea.Cmd {
	return m.Spinner.Tick
}

type installErrorMsg struct{ err error }
type installCompleteMsg struct{}
type taskProgressMsg struct {
	task     string
	progress float64
	step     int
	log      string
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case logMsg:
		m.Logs = append(m.Logs, "> "+string(msg))
		if len(m.Logs) > 10 {
			m.Logs = m.Logs[len(m.Logs)-10:]
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.Spinner, cmd = m.Spinner.Update(msg)
		return m, cmd

	case taskProgressMsg:
		m.CurrentTask = msg.task
		m.Progress = msg.progress
		return m, m.runInstallation(msg.step)

	case installErrorMsg:
		m.InstallError = msg.err
		m.Installing = false
		return m, nil

	case installCompleteMsg:
		m.Installing = false
		m.CurrentPage = PageComplete
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.Quitting = true
			return m, tea.Quit

		case "up", "k":
			if m.Cursor > 0 {
				m.Cursor--
			}
		case "down", "j":
			// Max cursor depends on page
			max := 0
			switch m.CurrentPage {
			case PageMode:
				max = 1
			case PageRedis:
				max = 1
			case PageRegistry:
				max = 4
			}
			if m.Cursor < max {
				m.Cursor++
			}

		case "enter":
			return m.handleNext()

		case "tab":
			if len(m.Inputs) > 0 {
				m.Focused = (m.Focused + 1) % len(m.Inputs)
				for i := range m.Inputs {
					if i == m.Focused {
						m.Inputs[i].Focus()
					} else {
						m.Inputs[i].Blur()
					}
				}
			}
		}
	}

	// Update text inputs if any are focused
	if len(m.Inputs) > 0 {
		var cmd tea.Cmd
		m.Inputs[m.Focused], cmd = m.Inputs[m.Focused].Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) handleNext() (Model, tea.Cmd) {
	switch m.CurrentPage {
	case PageWelcome:
		m.CurrentPage = PageMode
		m.Cursor = 0
	case PageMode:
		if m.Cursor == 0 {
			m.Mode = "dev"
		} else {
			m.Mode = "prod"
		}
		m.CurrentPage = PageRedis
		m.Cursor = 0
	case PageRedis:
		if m.Cursor == 0 {
			m.RedisBackend = "docker"
		} else {
			m.RedisBackend = "host"
		}
		m.CurrentPage = PageRegistry
		m.Cursor = 0
	case PageRegistry:
		types := []string{"local", "dockerhub", "ghcr", "ecr", "custom"}
		m.RegistryType = types[m.Cursor]
		if m.RegistryType == "local" {
			m.setupPortInputs()
			m.CurrentPage = PagePorts
		} else {
			m.setupCredentialInputs()
			m.CurrentPage = PageCredentials
		}
	case PageCredentials:
		m.RegistryUser = m.Inputs[0].Value()
		m.RegistryPass = m.Inputs[1].Value()
		m.setupPortInputs()
		m.CurrentPage = PagePorts
	case PagePorts:
		m.EnginePort = m.Inputs[0].Value()
		m.AgentPort = m.Inputs[1].Value()
		m.CurrentPage = PageSummary
	case PageSummary:
		m.CurrentPage = PageInstalling
		m.Installing = true
		m.CurrentTask = "Phase 0/10: Initializing environment..."

		return m, tea.Batch(
			m.Spinner.Tick,
			m.runInstallation(0),
		)
	case PageComplete:
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) runInstallation(step int) tea.Cmd {
	return func() tea.Msg {
		user := os.Getenv("USER")
		home, _ := os.UserHomeDir()
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
			user = sudoUser
			// If we are under sudo, os.UserHomeDir() might return /root.
			// Try to get the real user's home.
			if out, err := internal.RunCommand(fmt.Sprintf("getent passwd %s | cut -d: -f6", user)); err == nil {
				home = strings.TrimSpace(out)
			}
		}

		steps := []struct {
			name string
			fn   func() (string, error)
		}{
			{"Phase 0/10: Initializing environment...", func() (string, error) { return internal.InitializeInstaller() }},
			{"Phase 1/10: Installing system packages...", func() (string, error) { return internal.InstallPackages(m.RedisBackend) }},
			{"Phase 2/10: Setting up k3s...", func() (string, error) { return internal.InstallK3s(m.K8sNamespace) }},
			{"Phase 3/10: Configuring nerdctl & permissions...", func() (string, error) { return internal.InstallNerdctl(user) }},
			{"Phase 4/10: Configuring registry...", func() (string, error) { return internal.SetupRegistry(m.RegistryType, m.RegistryURL, m.RegistryUser, m.RegistryPass) }},
			{"Phase 5/10: Deploying Redis...", func() (string, error) { return internal.InstallRedis(m.RedisBackend, m.RedisURL) }},
			{"Phase 6/10: Setting up WireGuard...", func() (string, error) {
				if m.Mode == "prod" {
					return internal.SetupWireGuard()
				}
				return "Skipped (Dev mode)", nil
			}},
			{"Phase 7/10: Building & installing Nexus binaries...", func() (string, error) {
				cwd, _ := os.Getwd()
				repoRoot := cwd
				// Detect repo root by looking for nexus-engine
				for i := 0; i < 3; i++ {
					if _, err := os.Stat(filepath.Join(repoRoot, "nexus-engine")); err == nil {
						break
					}
					repoRoot = filepath.Dir(repoRoot)
				}
				return internal.BuildAndInstallBinaries(repoRoot)
			}},
			{"Phase 8/10: Writing Nexus configuration...", func() (string, error) {
				conf := internal.Config{
					Mode:          m.Mode,
					RedisBackend:  m.RedisBackend,
					RegistryType:  m.RegistryType,
					RegistryURL:   m.RegistryURL,
					RegistryUser:  m.RegistryUser,
					RegistryPass:  m.RegistryPass,
					EnginePort:    m.EnginePort,
					AgentPort:     m.AgentPort,
					K8sNamespace:  m.K8sNamespace,
					RedisURL:      m.RedisURL,
					NodeAgentAddr: "127.0.0.1:50051",
				}
				return internal.WriteConfigFile(home, conf)
			}},
			{"Phase 9/10: Orchestrating systemd services...", func() (string, error) {
				return internal.SetupServices(m.Mode, m.EnginePort, m.RedisURL, m.RegistryURL, "127.0.0.1:50051", m.K8sNamespace)
			}},
			{"Phase 10/10: Finalizing shell completion...", func() (string, error) {
				return internal.SetupShellCompletion(home)
			}},
		}

		if step >= len(steps) {
			return installCompleteMsg{}
		}

		out, err := steps[step].fn()
		if err != nil {
			return installErrorMsg{err}
		}

		nextTask := "Complete"
		if step+1 < len(steps) {
			nextTask = steps[step+1].name
		}

		return taskProgressMsg{
			task:     nextTask,
			progress: float64(step+1) / float64(len(steps)),
			step:     step + 1,
			log:      out,
		}
	}
}

func (m *Model) setupCredentialInputs() {
	m.Inputs = make([]textinput.Model, 2)
	m.Inputs[0] = textinput.New()
	m.Inputs[0].Placeholder = "Username"
	m.Inputs[0].Focus()
	
	m.Inputs[1] = textinput.New()
	m.Inputs[1].Placeholder = "Password / Token"
	m.Inputs[1].EchoMode = textinput.EchoPassword
	m.Focused = 0
}

func (m *Model) setupPortInputs() {
	m.Inputs = make([]textinput.Model, 2)
	m.Inputs[0] = textinput.New()
	m.Inputs[0].Placeholder = "Engine Port"
	m.Inputs[0].SetValue("8081")
	m.Inputs[0].Focus()
	
	m.Inputs[1] = textinput.New()
	m.Inputs[1].Placeholder = "Agent Port"
	m.Inputs[1].SetValue("50051")
	m.Focused = 0
}
