package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const bannerArt = `███╗   ██╗███████╗██╗  ██╗██╗   ██╗███████╗
████╗  ██║██╔════╝╚██╗██╔╝██║   ██║██╔════╝
██╔██╗ ██║█████╗   ╚███╔╝ ██║   ██║███████╗
██║╚██╗██║██╔══╝   ██╔██╗ ██║   ██║╚════██║
██║ ╚████║███████╗██╔╝ ██╗╚██████╔╝███████║
╚═╝  ╚═══╝╚══════╝╚═╝  ╚═╝ ╚═════╝ ╚══════╝`

func (m Model) View() string {
	if m.Quitting {
		return ""
	}

	var content string
	switch m.CurrentPage {
	case PageWelcome:
		content = m.renderWelcome()
	case PageMode:
		content = m.renderSelectionPage("Operating Mode", "Choose your operating mode:", []string{"dev", "prod"}, []string{"Development (local, allow-all networking)", "Production (VPN-only, strict isolation)"})
	case PageRedis:
		content = m.renderSelectionPage("Redis Backend", "How should Redis be deployed?", []string{"docker", "host"}, []string{"Docker container (recommended)", "System package (requires existing install)"})
	case PageRegistry:
		content = m.renderSelectionPage("Container Registry", "Where should challenge images be stored?", []string{"local", "dockerhub", "ghcr", "ecr", "custom"}, []string{"Local registry (localhost:5000)", "Docker Hub", "GitHub Container Registry", "AWS ECR", "Custom registry"})
	case PageCredentials:
		content = m.renderCredentialsPage()
	case PagePorts:
		content = m.renderPortsPage()
	case PageSummary:
		content = m.renderSummaryPage()
	case PageInstalling:
		content = m.renderInstallingPage()
	case PageComplete:
		content = m.renderCompletePage()
	}

	return content + "\n" + m.renderFooter()
}

func (m Model) renderWelcome() string {
	banner := StyleBrand.Render(bannerArt)
	title := lipgloss.NewStyle().Bold(true).Foreground(ColorWhite).Render("Nexus OSS — Challenge Infrastructure")
	subtitle := StyleStep.Render("Self-hosted isolated pod deployment for CTFs")

	welcome := lipgloss.JoinVertical(lipgloss.Center,
		banner,
		"",
		title,
		subtitle,
		"",
		StyleSuccess.Render("Press Enter to start installation..."),
	)

	return StyleBox.Render(welcome)
}

func (m Model) renderSelectionPage(title, subtitle string, options []string, help []string) string {
	var sb strings.Builder
	sb.WriteString(StyleHeader.Render("Step 1/7: "+title) + "\n\n")
	sb.WriteString(subtitle + "\n\n")

	for i, opt := range options {
		cursor := "  "
		style := StyleUnselected
		if m.Cursor == i {
			cursor = StyleBrand.Render("> ")
			style = StyleSelected
		}
		sb.WriteString(fmt.Sprintf("%s%s  %s\n", cursor, style.Render(fmt.Sprintf("%-10s", opt)), StyleGray.Render(help[i])))
	}

	return StyleBox.Render(sb.String())
}

func (m Model) renderCredentialsPage() string {
	var sb strings.Builder
	sb.WriteString(StyleHeader.Render("Step 4/7: Registry Credentials") + "\n\n")
	sb.WriteString(fmt.Sprintf("Configuring %s\n\n", strings.ToUpper(m.RegistryType)))

	labels := []string{"Username: ", "Password: "}
	for i := range m.Inputs {
		sb.WriteString(StyleInputPrompt.Render(labels[i]) + m.Inputs[i].View() + "\n")
	}

	return StyleBox.Render(sb.String())
}

func (m Model) renderPortsPage() string {
	var sb strings.Builder
	sb.WriteString(StyleHeader.Render("Step 5/7: Service Ports") + "\n\n")

	labels := []string{"Nexus Engine HTTP: ", "Node Agent gRPC:   "}
	for i := range m.Inputs {
		sb.WriteString(StyleInputPrompt.Render(labels[i]) + m.Inputs[i].View() + "\n")
	}

	return StyleBox.Render(sb.String())
}

func (m Model) renderSummaryPage() string {
	var sb strings.Builder
	sb.WriteString(StyleHeader.Render("Step 6/7: Review Configuration") + "\n\n")

	items := []struct{ k, v string }{
		{"Mode:", m.Mode},
		{"Redis:", m.RedisBackend},
		{"Registry:", m.RegistryType},
		{"Engine Port:", m.EnginePort},
		{"Agent Port:", m.AgentPort},
		{"Namespace:", m.K8sNamespace},
	}

	for _, item := range items {
		sb.WriteString(fmt.Sprintf("%-15s %s\n", StyleGray.Render(item.k), item.v))
	}

	sb.WriteString("\n" + StyleSuccess.Render("Press Enter to proceed with installation..."))

	return StyleBox.Render(sb.String())
}

func (m Model) renderInstallingPage() string {
	var sb strings.Builder
	sb.WriteString(StyleHeader.Render("Step 7/7: Installing") + "\n\n")

	prog := int(m.Progress * 20)
	bar := StyleBrand.Render(strings.Repeat("█", prog)) + StyleGray.Render(strings.Repeat("░", 20-prog))
	sb.WriteString(fmt.Sprintf("[%s] %d%%\n\n", bar, int(m.Progress*100)))

	if m.InstallError != nil {
		sb.WriteString(StyleError.Render("✘ ") + m.CurrentTask + "\n\n")
	} else {
		sb.WriteString(m.Spinner.View() + " " + m.CurrentTask + "\n\n")
	}

	if len(m.Logs) > 0 {
		sb.WriteString(StyleGray.Render("Logs:") + "\n")
		for _, log := range m.Logs {
			// Clean up output a bit
			cleanLog := strings.TrimSpace(log)
			if len(cleanLog) > 70 {
				cleanLog = cleanLog[:67] + "..."
			}
			sb.WriteString(StyleGray.Render("  > "+cleanLog) + "\n")
		}
	}

	if m.InstallError != nil {
		sb.WriteString("\n" + StyleError.Bold(true).Render("Error: "+m.InstallError.Error()) + "\n")
		sb.WriteString(StyleGray.Render("Check /var/log/nexus-install.log for details"))
	}

	return StyleBox.Render(sb.String())
}

func (m Model) renderCompletePage() string {
	title := StyleSuccess.Render("Setup Complete!")
	body := fmt.Sprintf("\nNexus OSS is ready to use.\n\nEndpoints:\n  Engine: http://localhost:%s\n  Agent:  grpc://localhost:%s\n\nConfiguration:\n  ~/.config/nexus/config.json\n\nPress Enter to exit", m.EnginePort, m.AgentPort)

	return StyleBox.Render(lipgloss.JoinVertical(lipgloss.Center, title, body))
}

func (m Model) renderFooter() string {
	return StyleFooter.Render("esc: quit • enter: next • arrows: navigate")
}
