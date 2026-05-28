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
	Port    int
	PID     int32
	Cmdline string
	User    string
}

var commonDevPorts = []int{3000, 3001, 3002, 4000, 5000, 5001, 8000, 5173, 8001, 8080, 8081, 9000, 9090}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	portStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("228"))
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
	mode      appMode
	textInput textinput.Model
	selected  *PortInfo
	status    string
	quitting  bool
	showPath  bool
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
			}
		}

		portMap[portNum] = info
	}

	ports := make([]PortInfo, 0, len(portMap))
	for _, info := range portMap {
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
		fmt.Printf("%s\t%s\t%s\t%s\n", portStr, pidStr, cmdStr, p.User)
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
	if err := killPID(info.PID); err != nil {
		return nil, err
	}
	return info, nil
}

func newModel(ports []PortInfo) model {
	ti := textinput.New()
	ti.Placeholder = "Enter any listening port, e.g. 5173"
	ti.CharLimit = 5
	ti.Width = 30

	return model{
		ports:     ports,
		textInput: ti,
		status:    "Select a listed dev port or press a to enter any port manually.",
	}
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
	case killResultMsg:
		if msg.err != nil {
			m.status = "Error: " + msg.err.Error()
		} else {
			m.status = msg.message
			m.ports, _ = scanDevPorts()
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
			m.status = "No listed dev ports are active. Press a to kill a custom port."
			return m, nil
		}
		selected := m.ports[m.cursor]
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
	b.WriteString("\n")
	b.WriteString("Common developer ports currently listening\n\n")

	if len(m.ports) == 0 {
		b.WriteString(helpStyle.Render("No common dev ports are active right now.\n"))
	} else {
		for i, p := range m.ports {
			cursor := " "
			if i == m.cursor && m.mode == modeBrowse {
				cursor = cursorStyle.Render(">")
			}

			line := fmt.Sprintf("%s %s  PID %-6d", cursor, portStyle.Render(fmt.Sprintf(":%d", p.Port)), p.PID)
			if m.showPath {
				cmdline := p.Cmdline
				if strings.TrimSpace(cmdline) == "" {
					cmdline = "(command unavailable)"
				}
				if len(cmdline) > 70 {
					cmdline = cmdline[:67] + "..."
				}
				line += "  " + cmdline
			}
			b.WriteString(line + "\n")
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
		b.WriteString(helpStyle.Render("Up/Down: move  Enter: kill selected  p: toggle path  a: add custom port  q: quit\n"))
	}

	if m.status != "" {
		b.WriteString("\n" + m.status + "\n")
	}

	return b.String()
}

func runInteractive(ports []PortInfo) error {
	p := tea.NewProgram(newModel(ports))
	_, err := p.Run()
	return err
}

func main() {
	killAll := flag.Bool("kill-all", false, "Kill all detected common developer ports")
	killPortStr := flag.String("kill", "", "Kill a specific listening port (e.g. --kill=3000)")
	yes := flag.Bool("yes", false, "Bypass confirmation prompts for non-interactive CLI kills")
	interactive := flag.Bool("interactive", false, "Open the interactive Bubble Tea UI")
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
		fmt.Println("Default view lists common developer ports; --kill accepts any listening port.")
		fmt.Println("Use -y/--yes to bypass confirmation prompts for non-interactive CLI kills.")
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

	ports, err := scanDevPorts()
	if err != nil {
		color.Red("Error: %s", err)
		os.Exit(1)
	}

	if *killAll {
		if len(ports) == 0 {
			color.Yellow("No developer ports found.")
			return
		}

		printPorts(ports)
		if *yes {
			for _, p := range ports {
				if err := killPID(p.PID); err != nil {
					color.Red("Failed to kill PID %d (port %d): %v", p.PID, p.Port, err)
				} else {
					color.Green("Killed PID %d (port %d)", p.PID, p.Port)
				}
			}
		} else {
			color.Yellow("Kill ALL developer ports? (y/N): ")
			var confirm string
			fmt.Scanln(&confirm)
			if strings.ToLower(strings.TrimSpace(confirm)) == "y" {
				for _, p := range ports {
					if err := killPID(p.PID); err != nil {
						color.Red("Failed to kill PID %d (port %d): %v", p.PID, p.Port, err)
					} else {
						color.Green("Killed PID %d (port %d)", p.PID, p.Port)
					}
				}
			}
		}
		return
	}

	if *interactive || (!*killAll && *killPortStr == "" && !*help) {
		if err := runInteractive(ports); err != nil {
			color.Red("Error: %v", err)
			os.Exit(1)
		}
		return
	}
}
