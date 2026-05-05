// Package tui implements the Nexus OSS Bubbletea TUI dashboard.
// Architecture: single model, polling goroutine sends tea.Msg, draw is pure.
package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nexus-oss/nexus/nexus-cli/client"
)

// ─── Styles ───────────────────────────────────────────────────────────────────

var (
	styleBrand = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00E5FF")).
			Padding(0, 1)

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#B0BEC5"))

	styleActive = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#69FF47"))

	styleDegraded = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFD600"))

	styleFailed = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5252"))

	styleSelected = lipgloss.NewStyle().
			Background(lipgloss.Color("#1A237E")).
			Foreground(lipgloss.Color("#FFFFFF")).
			Padding(0, 1)

	styleRow = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ECEFF1")).
			Padding(0, 1)

	styleFooter = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#546E7A")).
			BorderTop(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#263238"))

	styleMuted = lipgloss.NewStyle().Foreground(lipgloss.Color("#546E7A"))

	styleBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#263238")).
			Padding(0, 1)

	styleError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5252")).
			Bold(true)
)

// ─── Model ────────────────────────────────────────────────────────────────────

// tab identifies the active navigation tab.
type tab int

const (
	tabSessions tab = iota
	tabChallenges
	tabSystem
	tabMetrics
	tabController
	tabCluster
	tabRegistry
)

var tabNames = []string{"Sessions", "Challenges", "System", "Metrics", "Controller", "Cluster", "Registry"}

// snapshot is the data fetched from the engine on each poll cycle.
type snapshot struct {
	sessions    []client.Session
	challenges  []client.Challenge
	system      *client.SystemInfo
	controller  *client.ControllerStats
	health      *client.HealthResponse
	metrics     map[string]float64
	
	// Cluster data
	clusterNamespace string
	clusterPods      []client.ClusterPod
	clusterNodes     []client.ClusterNode
	clusterPolicies  []client.NetworkPolicy
	
	// Registry data
	registryImages []client.RegistryImage
	registryNote   string
	registryStats  *client.RegistryStats
	registryPulls  []client.RegistryPull

	fetchedAt   time.Time
	err         string
}

type tickMsg time.Time
type snapshotMsg snapshot

// Model is the Bubbletea model for the TUI.
type Model struct {
	client    *client.Client
	spinner   spinner.Model
	width     int
	height    int
	activeTab tab
	cursor    int
	loading   bool
	last      snapshot
}

// New creates a new TUI model.
func New(c *client.Client) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#00E5FF"))

	return Model{
		client:  c,
		spinner: sp,
		loading: true,
	}
}

// ─── Init / Update / View ─────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.fetchData(),
		tick(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case snapshotMsg:
		m.last = snapshot(msg)
		m.loading = false
		return m, tick()

	case tickMsg:
		return m, m.fetchData()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab", "right", "l":
		m.activeTab = tab((int(m.activeTab) + 1) % len(tabNames))
		m.cursor = 0
		return m, m.fetchData()
	case "shift+tab", "left", "h":
		m.activeTab = tab((int(m.activeTab) - 1 + len(tabNames)) % len(tabNames))
		m.cursor = 0
		return m, m.fetchData()
	case "j", "down":
		m.cursor++
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "r":
		m.loading = true
		return m, m.fetchData()
	case "1":
		m.activeTab = tabSessions
		m.cursor = 0
		return m, m.fetchData()
	case "2":
		m.activeTab = tabChallenges
		m.cursor = 0
		return m, m.fetchData()
	case "3":
		m.activeTab = tabSystem
		m.cursor = 0
		return m, m.fetchData()
	case "4":
		m.activeTab = tabMetrics
		m.cursor = 0
		return m, m.fetchData()
	case "5":
		m.activeTab = tabController
		m.cursor = 0
		return m, m.fetchData()
	case "6":
		m.activeTab = tabCluster
		m.cursor = 0
		return m, m.fetchData()
	case "7":
		m.activeTab = tabRegistry
		m.cursor = 0
		return m, m.fetchData()
	}
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return "initializing…"
	}
	sections := []string{
		m.renderHeader(),
		m.renderTabs(),
		m.renderBody(),
		m.renderFooter(),
	}
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// ─── Render helpers ────────────────────────────────────────────────────────────

func (m Model) renderHeader() string {
	banner := lipgloss.NewStyle().Foreground(lipgloss.Color("#00E5FF")).Render(`
  ███╗   ██╗███████╗██╗  ██╗██╗   ██╗███████╗
  ████╗  ██║██╔════╝╚██╗██╔╝██║   ██║██╔════╝
  ██╔██╗ ██║█████╗   ╚███╔╝ ██║   ██║███████╗
  ██║╚██╗██║██╔══╝   ██╔██╗ ██║   ██║╚════██║
  ██║ ╚████║███████╗██╔╝ ██╗╚██████╔╝███████║
  ╚═╝  ╚═══╝╚══════╝╚═╝  ╚═╝ ╚═════╝ ╚══════╝`)

	brand := styleBrand.Render("⬡ NEXUS OSS")
	mode := ""
	engineStatus := ""

	if m.last.health != nil {
		mode = styleMuted.Render("mode:" + m.last.health.Mode)
		engineStatus = styleActive.Render("● online")
	} else {
		engineStatus = styleFailed.Render("● offline")
	}

	fetched := ""
	if !m.last.fetchedAt.IsZero() {
		fetched = styleMuted.Render(fmt.Sprintf("updated %s ago", humanDuration(time.Since(m.last.fetchedAt))))
	}

	sp := ""
	if m.loading {
		sp = m.spinner.View()
	}

	header := lipgloss.JoinHorizontal(lipgloss.Center, brand, "  ", mode)
	status := lipgloss.JoinHorizontal(lipgloss.Center, sp, " ", engineStatus, "  ", fetched)

	pad := m.width - lipgloss.Width(header) - lipgloss.Width(status)
	if pad < 0 {
		pad = 0
	}

	top := header + strings.Repeat(" ", pad) + status

	return lipgloss.JoinVertical(lipgloss.Left,
		banner,
		"\n",
		lipgloss.NewStyle().
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#263238")).
			Width(m.width).
			Render(top),
	)
}

func (m Model) renderTabs() string {
	tabs := make([]string, len(tabNames))
	for i, name := range tabNames {
		label := fmt.Sprintf(" %s ", name)
		if tab(i) == m.activeTab {
			tabs[i] = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#00E5FF")).
				BorderBottom(true).
				BorderStyle(lipgloss.ThickBorder()).
				BorderForeground(lipgloss.Color("#00E5FF")).
				Render(label)
		} else {
			tabs[i] = styleMuted.Render(label)
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, tabs...) + "\n"
}

func (m Model) renderBody() string {
	bodyHeight := m.height - 12 // Adjusted for banner + header + tabs + footer
	if bodyHeight < 1 {
		bodyHeight = 10
	}

	if m.last.err != "" {
		return styleError.Render("⚠  " + m.last.err)
	}

	switch m.activeTab {
	case tabSessions:
		return m.renderSessions(bodyHeight)
	case tabChallenges:
		return m.renderChallenges(bodyHeight)
	case tabSystem:
		return m.renderSystem()
	case tabMetrics:
		return m.renderMetrics()
	case tabController:
		return m.renderController()
	case tabCluster:
		return m.renderCluster()
	case tabRegistry:
		return m.renderRegistry()
	}
	return ""
}

func (m Model) renderSessions(maxRows int) string {
	sessions := m.last.sessions
	header := styleHeader.Render(
		fmt.Sprintf("%-12s  %-10s  %-12s  %-15s  %-13s  %-8s  %s",
			"SESSION", "USER", "CHALLENGE", "POD IP", "STATUS", "EXPIRES", "ERR"))

	if len(sessions) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, header, styleMuted.Render("  no active sessions"))
	}

	rows := []string{header}
	for i, s := range sessions {
		if i >= maxRows-2 {
			rows = append(rows, styleMuted.Render(fmt.Sprintf("  … %d more sessions", len(sessions)-i)))
			break
		}

		status := renderStatus(s.Status)
		expires := humanDuration(time.Until(s.ExpiresAt))
		errStr := s.LastReconcileError
		if len(errStr) > 20 {
			errStr = errStr[:20] + "…"
		}

		line := fmt.Sprintf("%-12s  %-10s  %-12s  %-15s  %s  %-8s  %s",
			truncate(s.ID, 12),
			truncate(s.UserID, 10),
			truncate(s.ChallengeID, 12),
			s.PodIP,
			status,
			expires,
			errStr,
		)

		if i == m.cursor {
			rows = append(rows, styleSelected.Render(line))
		} else {
			rows = append(rows, styleRow.Render(line))
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m Model) renderChallenges(maxRows int) string {
	challenges := m.last.challenges
	header := styleHeader.Render(
		fmt.Sprintf("%-20s  %-8s  %-12s  %s",
			"NAME", "TTL", "PORTS", "IMAGE"))

	if len(challenges) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, header, styleMuted.Render("  no challenges registered"))
	}

	rows := []string{header}
	for i, ch := range challenges {
		if i >= maxRows-2 {
			rows = append(rows, styleMuted.Render(fmt.Sprintf("  … %d more", len(challenges)-i)))
			break
		}
		ports := formatPorts(ch.Ports)
		img := ch.Image
		if img == "" && len(ch.Containers) > 0 {
			img = styleMuted.Render(fmt.Sprintf("multi(%d services)", len(ch.Containers)))
		} else {
			img = truncate(img, 40)
		}

		line := fmt.Sprintf("%-20s  %-8s  %-12s  %s",
			truncate(ch.Name, 20),
			fmt.Sprintf("%dm", ch.TTLMinutes),
			ports,
			img)

		if i == m.cursor {
			rows = append(rows, styleSelected.Render(line))
		} else {
			rows = append(rows, styleRow.Render(line))
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m Model) renderSystem() string {
	if m.last.system == nil {
		return styleMuted.Render("  loading system info…")
	}
	s := m.last.system
	lines := []string{
		styleHeader.Render("System Overview"),
		"",
		fmt.Sprintf("  %-18s  %s", "Active Sessions:", styleActive.Render(fmt.Sprintf("%d", s.SessionsTotal))),
		fmt.Sprintf("  %-18s  %d", "Pods Running:", s.PodsTotal),
		fmt.Sprintf("  %-18s  %s", "Mode:", s.Mode),
		fmt.Sprintf("  %-18s  %s", "Registry:", s.Registry),
		fmt.Sprintf("  %-18s  %s", "Updated:", s.Timestamp),
	}
	if m.last.health != nil {
		lines = append(lines, fmt.Sprintf("  %-18s  %s", "Engine:", styleActive.Render("healthy")))
	} else {
		lines = append(lines, fmt.Sprintf("  %-18s  %s", "Engine:", styleFailed.Render("unreachable")))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderMetrics() string {
	if m.last.metrics == nil {
		return styleMuted.Render("  loading metrics…")
	}
	metrics := m.last.metrics

	renderBar := func(label string, value float64, unit string) string {
		return fmt.Sprintf("  %-22s %v%s", label+":", styleActive.Render(fmt.Sprintf("%.2f", value)), unit)
	}

	lines := []string{
		styleHeader.Render("Live Engine Metrics (Prometheus)"),
		"",
		renderBar("Goroutines", metrics["go_goroutines"], ""),
		renderBar("Heap Allocated", metrics["go_memstats_heap_alloc_bytes"]/(1024*1024), " MB"),
		renderBar("System Memory", metrics["go_memstats_sys_bytes"]/(1024*1024), " MB"),
		renderBar("Resident Memory", metrics["process_resident_memory_bytes"]/(1024*1024), " MB"),
		"",
		styleHeader.Render("Nexus Specifics"),
		"",
		renderBar("Reconcile Cycles", metrics["nexus_reconcile_cycles_total"], ""),
		renderBar("Node Agent Errors", metrics["nexus_nodeagent_rpc_errors_total"], ""),
		renderBar("Open File Descriptors", metrics["process_open_fds"], ""),
		renderBar("CPU Seconds Total", metrics["process_cpu_seconds_total"], "s"),
		"",
		styleHeader.Render("Node Agent Operations"),
		"",
	}

	if m.last.health != nil && m.last.health.Mode == "dev" {
		lines = append(lines, styleMuted.Render("  Not available in dev mode"))
	} else {
		// Mock/Placeholder for real network metrics if they existed in prometheus
		lines = append(lines, renderBar("Ipset Updates", metrics["nexus_ipset_updates_total"], ""))
		lines = append(lines, renderBar("WireGuard Resets", metrics["nexus_wireguard_resets_total"], ""))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderCluster() string {
	if m.last.clusterPods == nil && !m.loading {
		return styleMuted.Render("  loading cluster data…")
	}
	
	lines := []string{
		styleHeader.Render(fmt.Sprintf("Cluster Overview (Namespace: %s)", m.last.clusterNamespace)),
		"",
		styleHeader.Render("PODS"),
		fmt.Sprintf("%-30s  %-12s  %-6s  %-8s  %-8s", "NAME", "STATUS", "READY", "RESTARTS", "AGE"),
	}

	for _, p := range m.last.clusterPods {
		line := fmt.Sprintf("%-30s  %-12s  %-6s  %-8d  %s",
			truncate(p.Name, 30), p.Status, p.Ready, p.Restarts, humanDuration(time.Duration(p.AgeSeconds)*time.Second))
		lines = append(lines, styleRow.Render(line))
	}

	lines = append(lines, "", styleHeader.Render("NODES"))
	lines = append(lines, fmt.Sprintf("%-20s  %-10s  %-10s", "NAME", "STATUS", "CAPACITY"))
	for _, n := range m.last.clusterNodes {
		line := fmt.Sprintf("%-20s  %-10s  %d pods", n.Name, n.Status, n.PodsMax)
		lines = append(lines, styleRow.Render(line))
	}

	lines = append(lines, "", styleHeader.Render("NETWORK POLICIES"))
	for _, p := range m.last.clusterPolicies {
		lines = append(lines, styleRow.Render(fmt.Sprintf("  ● %-30s (%s)", p.Name, p.Status)))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderRegistry() string {
	if m.last.registryImages == nil && !m.loading {
		return styleMuted.Render("  loading registry data…")
	}

	lines := []string{
		styleHeader.Render("Registry Inventory"),
		"",
		fmt.Sprintf("%-30s  %-10s  %-10s", "IMAGE", "TAGS", "CREATED"),
	}

	for _, img := range m.last.registryImages {
		tags := strings.Join(img.Tags, ",")
		line := fmt.Sprintf("%-30s  %-10s  %s", truncate(img.Name, 30), truncate(tags, 10), humanDuration(time.Since(img.CreatedAt)))
		lines = append(lines, styleRow.Render(line))
	}

	if m.last.registryNote != "" {
		lines = append(lines, "", styleDegraded.Render("  "+m.last.registryNote))
	}

	if s := m.last.registryStats; s != nil {
		lines = append(lines, "", styleHeader.Render("STATS"))
		lines = append(lines, fmt.Sprintf("  Total Images:    %d", s.TotalImages))
		lines = append(lines, fmt.Sprintf("  Storage Used:    %d MB", s.TotalStorageMB))
		lines = append(lines, fmt.Sprintf("  Most Used:       %s (%d refs)", s.MostUsedImage, s.MostUsedRefs))
	}

	lines = append(lines, "", styleHeader.Render("PULL OPERATIONS (Last Hour)"))
	lines = append(lines, fmt.Sprintf("%-30s  %-8s  %-8s", "IMAGE", "PULLS", "SUCCESS"))
	for _, p := range m.last.registryPulls {
		line := fmt.Sprintf("%-30s  %-8d  %.1f%%", truncate(p.Image, 30), p.Pulls, p.SuccessRate)
		lines = append(lines, styleRow.Render(line))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderController() string {
	if m.last.controller == nil {
		return styleMuted.Render("  loading controller stats…")
	}
	cs := m.last.controller
	statusStyle := styleActive
	if cs.Status != "running" {
		statusStyle = styleDegraded
	}
	lines := []string{
		styleHeader.Render("Reconciliation Controller"),
		"",
		fmt.Sprintf("  %-18s  %s", "Status:", statusStyle.Render(cs.Status)),
		fmt.Sprintf("  %-18s  %d", "Workers:", cs.Workers),
		fmt.Sprintf("  %-18s  %s", "Interval:", cs.Interval),
		fmt.Sprintf("  %-18s  %d", "Queued:", cs.Queued),
		fmt.Sprintf("  %-18s  %d", "In Flight:", cs.InFlight),
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderFooter() string {
	keys := "  ↑/↓ navigate  ←/→ tabs  1-7 jump  r refresh  q quit"
	return styleFooter.Width(m.width).Render(keys)
}

// ─── Commands ─────────────────────────────────────────────────────────────────

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) fetchData() tea.Cmd {
	return func() tea.Msg {
		snap := snapshot{fetchedAt: time.Now(), metrics: make(map[string]float64)}

		// 1. Core health (Fast)
		if h, err := m.client.Health(); err == nil {
			snap.health = h
		} else {
			snap.err = "engine unreachable: " + err.Error()
			return snapshotMsg(snap)
		}

		// 2. Tab-specific data
		var err error
		switch m.activeTab {
		case tabSessions:
			snap.sessions, err = m.client.AdminSessions()
		case tabChallenges:
			snap.challenges, err = m.client.ListChallenges()
		case tabSystem:
			snap.system, err = m.client.SystemInfo()
		case tabController:
			snap.controller, err = m.client.ControllerStats()
		case tabCluster:
			var pods []client.ClusterPod
			snap.clusterNamespace, pods, err = m.client.GetClusterPods()
			snap.clusterPods = pods
			if err == nil {
				snap.clusterNodes, err = m.client.GetClusterNodes()
			}
			if err == nil {
				snap.clusterPolicies, err = m.client.GetNetworkPolicies()
			}
		case tabRegistry:
			snap.registryImages, snap.registryNote, err = m.client.GetRegistryImages()
			if err == nil {
				snap.registryStats, err = m.client.GetRegistryStats()
			}
			if err == nil {
				snap.registryPulls, err = m.client.GetRegistryPulls()
			}
		}

		if err != nil {
			snap.err = "API error: " + err.Error()
		}

		// 3. Metrics (Always for System/Metrics tabs)
		if m.activeTab == tabMetrics || m.activeTab == tabSystem {
			if raw, err := m.client.RawMetrics(); err == nil {
				lines := strings.Split(raw, "\n")
				for _, line := range lines {
					if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
						continue
					}
					parts := strings.Fields(line)
					if len(parts) == 2 {
						name := parts[0]
						if val, err := strconv.ParseFloat(parts[1], 64); err == nil {
							snap.metrics[name] = val
						}
					}
				}
			}
		}

		return snapshotMsg(snap)
	}
}

// ─── Utility ──────────────────────────────────────────────────────────────────

func renderStatus(status string) string {
	switch status {
	case "running":
		return styleActive.Render("● running    ")
	case "creating":
		return styleDegraded.Render("● creating   ")
	case "terminating":
		return styleDegraded.Render("● terminating")
	case "failed":
		return styleFailed.Render("● failed     ")
	case "expired":
		return styleFailed.Render("● expired    ")
	default:
		return styleMuted.Render("● " + status + "      ")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func formatPorts(ports []int) string {
	strs := make([]string, len(ports))
	for i, p := range ports {
		strs[i] = fmt.Sprintf("%d", p)
	}
	if len(strs) == 0 {
		return "—"
	}
	result := strings.Join(strs, ",")
	if len(result) > 12 {
		result = result[:11] + "…"
	}
	return result
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
