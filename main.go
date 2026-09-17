package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fatih/color"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

type PortInfo struct {
	Port          int
	PID           int32
	Cmdline       string
	User          string
	Name          string
	Protected     bool
	ProtectReason string
}

// Overridden at build time by the release workflow:
// go build -ldflags "-X main.version=v1.2.3".
var version = "dev"

var commonDevPorts = []int{3000, 3001, 3002, 4000, 5000, 5001, 8000, 5173, 8001, 8080, 8081, 9000, 9090}

// Ports above 1024 that belong to the OS or a built-in Windows service.
// Killing these breaks RPC, networking, discovery, or remote access.
var protectedPorts = map[int]string{
	135:  "Windows RPC endpoint mapper",
	137:  "NetBIOS name service",
	138:  "NetBIOS datagram service",
	139:  "NetBIOS session service",
	445:  "SMB file sharing",
	593:  "RPC over HTTP",
	1900: "SSDP/UPnP discovery",
	2869: "Windows ICS / UPnP host",
	3389: "Remote Desktop (RDP)",
	3702: "WS-Discovery",
	5040: "Windows Connected Devices Platform",
	5353: "mDNS responder",
	5355: "LLMNR responder",
	5357: "WSDAPI (HTTP.sys)",
	5985: "WinRM over HTTP",
	5986: "WinRM over HTTPS",
	7680: "Windows Delivery Optimization",
}

var systemProcessNames = map[string]string{
	"system":              "Windows kernel",
	"system idle process": "Windows idle process",
	"registry":            "Windows registry",
	"memory compression":  "Windows memory manager",
	"smss.exe":            "Windows session manager",
	"csrss.exe":           "Windows client/server runtime",
	"wininit.exe":         "Windows start-up",
	"winlogon.exe":        "Windows logon",
	"services.exe":        "Windows service control manager",
	"lsass.exe":           "Windows local security authority",
	"svchost.exe":         "Windows service host",
	"fontdrvhost.exe":     "Windows font driver host",
	"dwm.exe":             "Windows desktop window manager",
	"spoolsv.exe":         "Windows print spooler",
	"audiodg.exe":         "Windows audio device graph",
	"msmpeng.exe":         "Windows Defender",
	"nissrv.exe":          "Windows Defender network inspection",
}

// Windows accounts that own OS services; gopsutil reports a raw SID when it
// cannot resolve the name.
var systemUsers = map[string]string{
	"system":                        "SYSTEM",
	"nt authority\\system":          "SYSTEM",
	"s-1-5-18":                      "SYSTEM",
	"local service":                 "LOCAL SERVICE",
	"nt authority\\local service":   "LOCAL SERVICE",
	"s-1-5-19":                      "LOCAL SERVICE",
	"network service":               "NETWORK SERVICE",
	"nt authority\\network service": "NETWORK SERVICE",
	"s-1-5-20":                      "NETWORK SERVICE",
	"root":                          "root",
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	portStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("228"))
	warnMark    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	lockedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

type appMode int

const (
	modeBrowse appMode = iota
	modeCustomInput
	modeConfirm
)

type killResultMsg struct {
	message string
	err     error
}

type model struct {
	ports     []PortInfo
	cursor    int
	height    int
	mode      appMode
	textInput textinput.Model
	selected  *PortInfo
	status    string
	quitting  bool
	showPath  bool
}

// protectReason explains why a port must not be killed, or returns "" when it
// is safe to terminate.
func protectReason(info PortInfo) string {
	if info.PID <= 0 {
		return "owner PID is not visible (run as Administrator for details)"
	}
	if info.PID == int32(os.Getpid()) || info.PID == int32(os.Getppid()) {
		return "it belongs to gkport or the shell that launched it"
	}
	if info.Port < 1024 {
		return fmt.Sprintf("port %d is a privileged port reserved for OS services", info.Port)
	}
	if what, ok := protectedPorts[info.Port]; ok {
		return what
	}
	// Windows only lets a non-elevated process inspect its own session. Anything
	// running in session 0 (services, lsass, RPC endpoints, antivirus) comes back
	// blank, so we cannot prove it is safe to kill.
	if strings.TrimSpace(info.Name) == "" {
		return "the owning process runs as a Windows service and is not readable without Administrator rights"
	}
	if what, ok := systemProcessNames[strings.ToLower(strings.TrimSpace(info.Name))]; ok {
		return what
	}
	if what, ok := systemUsers[strings.ToLower(strings.TrimSpace(info.User))]; ok {
		return "it is owned by the " + what + " account"
	}
	return ""
}

func scanListeningPorts() ([]PortInfo, error) {
	conns, err := net.Connections("tcp")
	if err != nil {
		return nil, fmt.Errorf("failed to get connections: %w", err)
	}

	portMap := make(map[int]PortInfo)
	for _, conn := range conns {
		if conn.Status != "LISTEN" || conn.Laddr.Port == 0 {
			continue
		}

		portNum := int(conn.Laddr.Port)
		info := PortInfo{
			Port: portNum,
			PID:  conn.Pid,
		}

		if info.PID > 0 {
			proc, err := process.NewProcess(info.PID)
			if err == nil {
				info.Cmdline, _ = proc.Cmdline()
				info.User, _ = proc.Username()
				info.Name, _ = proc.Name()
			}
		}

		// Dual-stack servers listen on IPv4 and IPv6; keep the entry whose PID
		// we could resolve so the port stays actionable.
		if previous, ok := portMap[portNum]; ok && previous.PID > 0 {
			continue
		}
		portMap[portNum] = info
	}

	ports := make([]PortInfo, 0, len(portMap))
	for _, info := range portMap {
		if reason := protectReason(info); reason != "" {
			info.Protected = true
			info.ProtectReason = reason
		}
		ports = append(ports, info)
	}

	sort.Slice(ports, func(i, j int) bool {
		return ports[i].Port < ports[j].Port
	})

	return ports, nil
}

func scanDevPorts() ([]PortInfo, error) {
	allPorts, err := scanListeningPorts()
	if err != nil {
		return nil, err
	}

	devPortSet := make(map[int]struct{}, len(commonDevPorts))
	for _, port := range commonDevPorts {
		devPortSet[port] = struct{}{}
	}

	filtered := make([]PortInfo, 0, len(allPorts))
	for _, info := range allPorts {
		if _, ok := devPortSet[info.Port]; ok {
			filtered = append(filtered, info)
		}
	}

	return filtered, nil
}

func findListeningPort(portNum int) (*PortInfo, error) {
	ports, err := scanListeningPorts()
	if err != nil {
		return nil, err
	}

	for _, p := range ports {
		if p.Port == portNum {
			port := p
			return &port, nil
		}
	}

	return nil, nil
}

func printPorts(ports []PortInfo) {
	if len(ports) == 0 {
		color.Yellow("No developer ports found.")
		return
	}

	color.Cyan("Developer Ports (LISTENING):")
	color.Cyan("Port\tPID\tCmdline\t\tUser")
	for _, p := range ports {
		portStr := color.YellowString("%d", p.Port)
		pidStr := fmt.Sprintf("%d", p.PID)
		cmdStr := color.GreenString("%s", p.Cmdline)
		mark := ""
		if p.Protected {
			mark = color.RedString("  ⚠ protected")
		}
		fmt.Printf("%s\t%s\t%s\t%s%s\n", portStr, pidStr, cmdStr, p.User, mark)
	}
}

func killPID(pid int32) error {
	proc, err := process.NewProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to open process %d: %w", pid, err)
	}

	if err := proc.Kill(); err != nil {
		c := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(int(pid)))
		if fallbackErr := c.Run(); fallbackErr != nil {
			return fallbackErr
		}
	}

	return nil
}

func killPortByNumber(portNum int) (*PortInfo, error) {
	info, err := findListeningPort(portNum)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, fmt.Errorf("port %d is not listening", portNum)
	}
	if info.PID <= 0 {
		return nil, fmt.Errorf("port %d has no associated PID", portNum)
	}
	if info.Protected {
		return nil, fmt.Errorf("port %d is protected: %s", info.Port, info.ProtectReason)
	}
	if err := killPID(info.PID); err != nil {
		return nil, err
	}
	return info, nil
}

// killPorts terminates every unprotected port, reporting the ones it refuses
// to touch so the skip is never silent.
func killPorts(ports []PortInfo) {
	for _, p := range ports {
		if p.Protected {
			color.Yellow("⚠ Skipped port %d: %s", p.Port, p.ProtectReason)
			continue
		}
		if err := killPID(p.PID); err != nil {
			color.Red("Failed to kill PID %d (port %d): %v", p.PID, p.Port, err)
		} else {
			color.Green("Killed PID %d (port %d)", p.PID, p.Port)
		}
	}
}

func countProtected(ports []PortInfo) int {
	count := 0
	for _, p := range ports {
		if p.Protected {
			count++
		}
	}
	return count
}

func protectedNotice(p PortInfo) string {
	return fmt.Sprintf("⚠ Port %d is protected: %s. gkport will not kill it.", p.Port, p.ProtectReason)
}

func shortCmdline(p PortInfo) string {
	cmdline := strings.TrimSpace(p.Cmdline)
	if cmdline == "" {
		return "(command unavailable)"
	}
	if len(cmdline) > 70 {
		return cmdline[:67] + "..."
	}
	return cmdline
}

func newModel(ports []PortInfo) model {
	ti := textinput.New()
	ti.Placeholder = "Enter any listening port, e.g. 5173"
	ti.CharLimit = 5
	ti.Width = 30

	return model{
		ports:     ports,
		textInput: ti,
		status:    "Select a port to kill, or press a to enter one manually.",
	}
}

func (m model) visibleRows() int {
	if m.height <= 0 {
		return 15
	}
	return max(m.height-7, 3)
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.mode {
		case modeBrowse:
			return m.updateBrowse(msg)
		case modeCustomInput:
			return m.updateCustomInput(msg)
		case modeConfirm:
			return m.updateConfirm(msg)
		}
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil
	case killResultMsg:
		if msg.err != nil {
			m.status = "Error: " + msg.err.Error()
		} else {
			m.status = msg.message
			m.ports, _ = scanListeningPorts()
			if m.cursor >= len(m.ports) && m.cursor > 0 {
				m.cursor--
			}
		}
		m.mode = modeBrowse
		m.selected = nil
		return m, nil
	}

	return m, nil
}

func (m model) updateBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.ports)-1 {
			m.cursor++
		}
	case "p":
		m.showPath = !m.showPath
		if m.showPath {
			m.status = "Path display enabled."
		} else {
			m.status = "Path display disabled."
		}
		return m, nil
	case "a":
		m.mode = modeCustomInput
		m.textInput.SetValue("")
		m.textInput.Focus()
		m.status = "Enter a listening port number to kill."
		return m, textinput.Blink
	case "enter":
		if len(m.ports) == 0 {
			m.status = "Nothing is listening. Press a to enter a port manually."
			return m, nil
		}
		selected := m.ports[m.cursor]
		if selected.Protected {
			m.status = protectedNotice(selected)
			return m, nil
		}
		m.selected = &selected
		m.mode = modeConfirm
		m.status = fmt.Sprintf("Confirm kill for port %d (PID %d).", selected.Port, selected.PID)
	}

	return m, nil
}

func (m model) updateCustomInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeBrowse
		m.textInput.Blur()
		m.status = "Returned to the port list."
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.textInput.Value())
		portNum, err := strconv.Atoi(value)
		if err != nil || portNum <= 0 || portNum > 65535 {
			m.status = "Enter a valid TCP port between 1 and 65535."
			return m, nil
		}

		info, err := findListeningPort(portNum)
		if err != nil {
			m.status = "Error: " + err.Error()
			return m, nil
		}
		if info == nil {
			m.status = fmt.Sprintf("Port %d is not currently listening.", portNum)
			return m, nil
		}
		if info.Protected {
			m.textInput.Blur()
			m.mode = modeBrowse
			m.status = protectedNotice(*info)
			return m, nil
		}

		m.selected = info
		m.mode = modeConfirm
		m.textInput.Blur()
		m.status = fmt.Sprintf("Confirm kill for custom port %d (PID %d).", info.Port, info.PID)
		return m, nil
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		if m.selected == nil {
			m.mode = modeBrowse
			return m, nil
		}
		portNum := m.selected.Port
		return m, func() tea.Msg {
			info, err := killPortByNumber(portNum)
			if err != nil {
				return killResultMsg{err: err}
			}
			return killResultMsg{message: fmt.Sprintf("Killed port %d (PID %d).", info.Port, info.PID)}
		}
	case "n", "esc":
		m.mode = modeBrowse
		m.selected = nil
		m.status = "Kill cancelled."
	}

	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("GKPort"))
	b.WriteString(helpStyle.Render(" " + version))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Listening TCP ports: %d", len(m.ports)))
	if protected := countProtected(m.ports); protected > 0 {
		b.WriteString(warnStyle.Render(fmt.Sprintf("   ⚠ %d protected and cannot be killed", protected)))
	}
	b.WriteString("\n\n")

	rows := m.visibleRows()
	start := max(0, m.cursor-rows+1)
	end := min(len(m.ports), start+rows)

	if len(m.ports) == 0 {
		b.WriteString(helpStyle.Render("Nothing is listening on TCP right now.\n"))
	} else {
		for i := start; i < end; i++ {
			p := m.ports[i]
			cursor := " "
			if i == m.cursor && m.mode == modeBrowse {
				cursor = cursorStyle.Render(">")
			}

			// Pad the warning column explicitly so the PID column stays aligned
			// whether or not the terminal renders ⚠ as a double-width glyph.
			marker := "  "
			portLabel := portStyle.Render(fmt.Sprintf(":%-5d", p.Port))
			if p.Protected {
				warn := warnMark.Render("⚠")
				marker = warn + strings.Repeat(" ", max(0, 2-lipgloss.Width(warn)))
				portLabel = lockedStyle.Render(fmt.Sprintf(":%-5d", p.Port))
			}

			line := fmt.Sprintf("%s %s%s PID %-6d", cursor, marker, portLabel, p.PID)
			if m.showPath {
				line += "  " + shortCmdline(p)
			}
			b.WriteString(line + "\n")
		}
		if start > 0 || end < len(m.ports) {
			b.WriteString(helpStyle.Render(fmt.Sprintf("  showing %d-%d of %d\n", start+1, end, len(m.ports))))
		}
	}

	b.WriteString("\n")

	switch m.mode {
	case modeCustomInput:
		b.WriteString("Custom port: " + m.textInput.View() + "\n")
		b.WriteString(helpStyle.Render("Enter confirms. Esc goes back.\n"))
	case modeConfirm:
		if m.selected != nil {
			b.WriteString(fmt.Sprintf("Kill port %d (PID %d)? Press y to confirm, n to cancel.\n", m.selected.Port, m.selected.PID))
		}
	default:
		b.WriteString(helpStyle.Render("Up/Down: move  Enter: kill selected  p: toggle path  a: custom port  q: quit\n"))
	}

	if m.mode == modeBrowse && m.cursor < len(m.ports) && m.ports[m.cursor].Protected {
		b.WriteString("\n" + warnStyle.Render(protectedNotice(m.ports[m.cursor])) + "\n")
	} else if m.status != "" {
		b.WriteString("\n" + m.status + "\n")
	}

	return b.String()
}

func runInteractive(ports []PortInfo) error {
	p := tea.NewProgram(newModel(ports), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func main() {
	killAll := flag.Bool("kill-all", false, "Kill all detected common developer ports")
	killPortStr := flag.String("kill", "", "Kill a specific listening port (e.g. --kill=3000)")
	yes := flag.Bool("yes", false, "Bypass confirmation prompts for non-interactive CLI kills")
	// Kept for backwards compatibility: the TUI is already the default view.
	flag.Bool("interactive", false, "Open the interactive Bubble Tea UI")
	help := flag.Bool("help", false, "Show help")
	// Short form: -y
	flag.BoolVar(yes, "y", false, "Bypass confirmation prompts for non-interactive CLI kills")
	flag.Parse()

	if *help {
		fmt.Println("gkport - Developer port lister & killer")
		fmt.Println("")
		fmt.Println("Usage:")
		fmt.Println("  gkport")
		fmt.Println("    [--interactive]")
		fmt.Println("    [--kill-all]")
		fmt.Println("    [--kill=PORT]")
		fmt.Println("    [-y|--yes]")
		fmt.Println("    [--help]")
		fmt.Println("")
		fmt.Println("Default view lists every listening TCP port and refuses to kill OS/system ports")
		fmt.Println("(privileged ports, known Windows service ports, and processes owned by SYSTEM).")
		fmt.Println("--kill-all only targets the common developer ports.")
		fmt.Println("Use -y/--yes to bypass confirmation prompts for non-interactive CLI kills.")
		fmt.Println("")
		fmt.Printf("Version: %s\n", version)
		os.Exit(0)
	}

	if *killPortStr != "" {
		portNum, err := strconv.Atoi(*killPortStr)
		if err != nil || portNum <= 0 || portNum > 65535 {
			color.Red("Invalid port: %s", *killPortStr)
			os.Exit(1)
		}

		info, err := findListeningPort(portNum)
		if err != nil {
			color.Red("Error: %v", err)
			os.Exit(1)
		}
		if info == nil {
			color.Yellow("Port %d is not currently listening.", portNum)
			os.Exit(1)
		}

		if info.Protected {
			color.Red("⚠ Refusing to kill port %d: %s.", info.Port, info.ProtectReason)
			os.Exit(1)
		}

		if *yes {
			if err := killPID(info.PID); err != nil {
				color.Red("Failed: %v", err)
				os.Exit(1)
			}
			color.Green("Killed port %d (PID %d)", info.Port, info.PID)
		} else {
			color.Yellow("Kill port %d (PID %d, %s)? (y/N): ", info.Port, info.PID, info.Cmdline)
			var confirm string
			fmt.Scanln(&confirm)
			if strings.ToLower(strings.TrimSpace(confirm)) == "y" {
				if err := killPID(info.PID); err != nil {
					color.Red("Failed: %v", err)
					os.Exit(1)
				}
				color.Green("Killed port %d (PID %d)", info.Port, info.PID)
			}
		}
		return
	}

	if *killAll {
		ports, err := scanDevPorts()
		if err != nil {
			color.Red("Error: %s", err)
			os.Exit(1)
		}
		if len(ports) == 0 {
			color.Yellow("No developer ports found.")
			return
		}

		printPorts(ports)
		if *yes {
			killPorts(ports)
		} else {
			color.Yellow("Kill ALL unprotected developer ports? (y/N): ")
			var confirm string
			fmt.Scanln(&confirm)
			if strings.ToLower(strings.TrimSpace(confirm)) == "y" {
				killPorts(ports)
			}
		}
		return
	}

	ports, err := scanListeningPorts()
	if err != nil {
		color.Red("Error: %s", err)
		os.Exit(1)
	}

	if err := runInteractive(ports); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
}
