package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fatih/color"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

type PortInfo struct {
	Port          int     `json:"port"`
	PID           int32   `json:"pid"`
	Cmdline       string  `json:"cmdline"`
	User          string  `json:"user"`
	Name          string  `json:"name"`
	CreateTime    int64   `json:"create_time_ms"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemRSS        uint64  `json:"mem_rss_bytes"`
	Protected     bool    `json:"protected"`
	ProtectReason string  `json:"protect_reason,omitempty"`
}

// Overridden at build time by the release workflow:
// go build -ldflags "-X main.version=v1.2.3".
var version = "v0.4.0"

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
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	portStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("228"))
	warnMark    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	lockedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

type appMode int

const (
	modeBrowse appMode = iota
	modeCustomInput
	modeConfirm
	modeFilter
)

type killResultMsg struct {
	message string
	err     error
	port    int
}

type model struct {
	ports     []PortInfo
	cursor    int
	height    int
	width     int
	mode      appMode
	textInput textinput.Model
	selected  *PortInfo
	status    string
	quitting  bool
	showPath  bool
	showInfo  bool
	killing   bool
	filter    string
	sortKey   string
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
				info.CreateTime, _ = proc.CreateTime()
				info.CPUPercent, _ = proc.CPUPercent()
				if mi, err := proc.MemoryInfo(); err == nil && mi != nil {
					info.MemRSS = mi.RSS
				}
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

// waitPortGone blocks until the port leaves the TCP table (or the timeout
// expires). Windows releases a terminated process's sockets asynchronously,
// so an immediate rescan would still find the dead owner's LISTEN entry with
// an unresolvable name and render it as "(unknown)".
func waitPortGone(portNum int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := findListeningPort(portNum)
		if err != nil || info == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
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
	waitPortGone(portNum, 3*time.Second)
	return info, nil
}

// killPorts terminates every unprotected port, reporting the ones it refuses
// to touch so the skip is never silent. It returns the number of failed kills.
// A PID that owns several ports is killed once.
func killPorts(ports []PortInfo, dryRun bool) int {
	killed := make(map[int32]bool)
	failed := 0
	for _, p := range ports {
		if p.Protected {
			color.Yellow("⚠ Skipped port %d: %s", p.Port, p.ProtectReason)
			continue
		}
		if dryRun {
			color.Cyan("[dry-run] Would kill PID %d (port %d)", p.PID, p.Port)
			continue
		}
		if killed[p.PID] {
			color.Yellow("Note: PID %d already terminated (also owned port %d)", p.PID, p.Port)
			continue
		}
		if err := killPID(p.PID); err != nil {
			color.Red("Failed to kill PID %d (port %d): %v", p.PID, p.Port, err)
			failed++
			continue
		}
		killed[p.PID] = true
		color.Green("Killed PID %d (port %d)", p.PID, p.Port)
	}
	return failed
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

// Session-0 processes stay unreadable without Administrator rights, so the
// name can legitimately come back empty.
func shortName(p PortInfo) string {
	if name := strings.TrimSpace(p.Name); name != "" {
		return name
	}
	return "(unknown)"
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

func sortPorts(ports []PortInfo, key string) {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "pid":
		sort.SliceStable(ports, func(i, j int) bool {
			if ports[i].PID != ports[j].PID {
				return ports[i].PID < ports[j].PID
			}
			return ports[i].Port < ports[j].Port
		})
	case "name":
		sort.SliceStable(ports, func(i, j int) bool {
			ni := strings.ToLower(shortName(ports[i]))
			nj := strings.ToLower(shortName(ports[j]))
			if ni != nj {
				return ni < nj
			}
			return ports[i].Port < ports[j].Port
		})
	default:
		sort.SliceStable(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	}
}

// parsePortSpec accepts "3000" or an inclusive range "3000-3010".
func parsePortSpec(spec string) ([]int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("empty port spec")
	}
	if i := strings.Index(spec, "-"); i > 0 && i < len(spec)-1 {
		lo, err1 := strconv.Atoi(strings.TrimSpace(spec[:i]))
		hi, err2 := strconv.Atoi(strings.TrimSpace(spec[i+1:]))
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("invalid range %q", spec)
		}
		if lo < 1 || hi > 65535 || lo > hi {
			return nil, fmt.Errorf("range %d-%d is out of bounds", lo, hi)
		}
		if hi-lo > 1000 {
			return nil, fmt.Errorf("range %d-%d spans more than 1000 ports", lo, hi)
		}
		ports := make([]int, 0, hi-lo+1)
		for p := lo; p <= hi; p++ {
			ports = append(ports, p)
		}
		return ports, nil
	}
	port, err := strconv.Atoi(spec)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid port %q", spec)
	}
	return []int{port}, nil
}

func printJSON(ports []PortInfo) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ports); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
}

func printCSV(ports []PortInfo) {
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()
	if err := w.Write([]string{"port", "pid", "name", "user", "protected", "reason"}); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
	for _, p := range ports {
		if err := w.Write([]string{
			strconv.Itoa(p.Port),
			strconv.Itoa(int(p.PID)),
			shortName(p),
			p.User,
			strconv.FormatBool(p.Protected),
			p.ProtectReason,
		}); err != nil {
			color.Red("Error: %v", err)
			os.Exit(1)
		}
	}
	if err := w.Error(); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
}

func humanDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func detailsSuffix(p PortInfo) string {
	if p.CreateTime <= 0 {
		return ""
	}
	mem := fmt.Sprintf("%.1fMB", float64(p.MemRSS)/(1024*1024))
	return fmt.Sprintf(" · up %s · %s · %.1f%% cpu", humanDur(time.Since(time.UnixMilli(p.CreateTime))), mem, p.CPUPercent)
}

func newModel(ports []PortInfo) model {
	ti := textinput.New()
	ti.Placeholder = "Enter any listening port, e.g. 5173"
	ti.CharLimit = 5
	ti.Width = 30

	return model{
		ports:     ports,
		textInput: ti,
		sortKey:   "port",
		status:    "Select a port to kill, or press a to enter one manually.",
	}
}

func (m model) visibleRows() int {
	if m.height <= 0 {
		return 15
	}
	return max(m.height-7, 3)
}

// visiblePorts applies the live filter over the scanned ports; navigation and
// rendering both operate on this slice.
func (m model) visiblePorts() []PortInfo {
	if m.filter == "" {
		return m.ports
	}
	f := strings.ToLower(m.filter)
	out := make([]PortInfo, 0, len(m.ports))
	for _, p := range m.ports {
		if strings.Contains(strings.ToLower(shortName(p)), f) ||
			strings.Contains(strings.ToLower(shortCmdline(p)), f) ||
			strings.Contains(strings.ToLower(p.User), f) ||
			strings.Contains(strconv.Itoa(p.Port), f) {
			out = append(out, p)
		}
	}
	return out
}

func (m *model) clampCursor() {
	n := len(m.visiblePorts())
	switch {
	case n == 0:
		m.cursor = 0
	case m.cursor >= n:
		m.cursor = n - 1
	case m.cursor < 0:
		m.cursor = 0
	}
}

func (m *model) cycleSort() {
	switch m.sortKey {
	case "port":
		m.sortKey = "pid"
	case "pid":
		m.sortKey = "name"
	default:
		m.sortKey = "port"
	}
	sortPorts(m.ports, m.sortKey)
	m.status = "Sorted by " + m.sortKey + "."
	m.clampCursor()
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Ctrl+C must quit from every mode; a bare "q" stays mode-local so it
		// remains typeable in the text inputs.
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
		switch m.mode {
		case modeBrowse:
			return m.updateBrowse(msg)
		case modeCustomInput:
			return m.updateCustomInput(msg)
		case modeConfirm:
			return m.updateConfirm(msg)
		case modeFilter:
			return m.updateFilter(msg)
		}
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		return m, nil
	case killResultMsg:
		m.killing = false
		if msg.err != nil {
			m.status = "Error: " + errText(msg.err)
		} else {
			m.status = msg.message
			m.ports, _ = scanListeningPorts()
			m.clampCursor()
		}
		m.mode = modeBrowse
		m.selected = nil
		return m, nil
	}

	return m, nil
}

// errText adds the actionable hint for the failure users actually hit.
func errText(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "Access is denied") {
		return msg + " — try running gkport as Administrator"
	}
	return msg
}

func (m model) updateBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	visible := m.visiblePorts()
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(visible)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = max(0, len(visible)-1)
	case "pgup":
		m.cursor = max(0, m.cursor-m.visibleRows())
	case "pgdown":
		m.cursor = min(len(visible)-1, m.cursor+m.visibleRows())
	case "p":
		m.showPath = !m.showPath
		if m.showPath {
			m.status = "Path display enabled."
		} else {
			m.status = "Path display disabled."
		}
		return m, nil
	case "i":
		m.showInfo = !m.showInfo
		if m.showInfo {
			m.status = "Process details enabled."
		} else {
			m.status = "Process details disabled."
		}
		return m, nil
	case "s":
		m.cycleSort()
		return m, nil
	case "/":
		m.mode = modeFilter
		m.textInput.SetValue("")
		m.textInput.Placeholder = "Filter by port, name, or command…"
		m.textInput.Focus()
		m.status = "Type to filter. Enter applies, Esc clears."
		return m, textinput.Blink
	case "a":
		m.mode = modeCustomInput
		m.textInput.SetValue("")
		m.textInput.Placeholder = "Enter any listening port, e.g. 5173"
		m.textInput.Focus()
		m.status = "Enter a listening port number to kill."
		return m, textinput.Blink
	case "enter":
		if len(visible) == 0 {
			if m.filter != "" {
				m.status = fmt.Sprintf("No port matches filter %q.", m.filter)
			} else {
				m.status = "Nothing is listening. Press a to enter a port manually."
			}
			return m, nil
		}
		selected := visible[m.cursor]
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

func (m model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeBrowse
		m.textInput.Blur()
		m.filter = ""
		m.clampCursor()
		m.status = "Filter cleared."
		return m, nil
	case "enter":
		m.mode = modeBrowse
		m.textInput.Blur()
		m.status = "Filter applied."
		m.clampCursor()
		return m, nil
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	m.filter = strings.TrimSpace(m.textInput.Value())
	m.clampCursor()
	return m, cmd
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
	if strings.ToLower(msg.String()) == "y" {
		if m.killing || m.selected == nil {
			m.mode = modeBrowse
			m.selected = nil
			return m, nil
		}
		m.killing = true
		m.status = fmt.Sprintf("Killing port %d (PID %d)…", m.selected.Port, m.selected.PID)
		portNum := m.selected.Port
		return m, func() tea.Msg {
			info, err := killPortByNumber(portNum)
			if err != nil {
				return killResultMsg{err: err, port: portNum}
			}
			return killResultMsg{message: fmt.Sprintf("Killed port %d (PID %d).", info.Port, info.PID), port: info.Port}
		}
	}
	// Any other key cancels; a stray keypress must never fire a kill.
	m.mode = modeBrowse
	m.selected = nil
	m.status = "Kill cancelled."
	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	if m.width > 0 && (m.width < 40 || m.height < 10) {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			warnStyle.Render("Terminal too small — resize to at least 40x10"))
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("GKPort"))
	b.WriteString(helpStyle.Render(" " + version))
	b.WriteString("\n")
	visible := m.visiblePorts()
	b.WriteString(fmt.Sprintf("Listening TCP ports: %d", len(visible)))
	if m.filter != "" {
		b.WriteString(helpStyle.Render(fmt.Sprintf("  (filter %q: %d/%d)", m.filter, len(visible), len(m.ports))))
	}
	if protected := countProtected(visible); protected > 0 {
		b.WriteString(warnStyle.Render(fmt.Sprintf("   ⚠ %d protected and cannot be killed", protected)))
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("⚠ = protected: OS/service-owned — gkport will never kill it"))
	b.WriteString("\n\n")

	rows := m.visibleRows()
	start := max(0, m.cursor-rows+1)
	end := min(len(visible), start+rows)

	if len(visible) == 0 {
		if m.filter != "" {
			b.WriteString(helpStyle.Render(fmt.Sprintf("Nothing matches filter %q. Press Esc to clear it.\n", m.filter)))
		} else {
			b.WriteString(helpStyle.Render("Nothing is listening on TCP right now.\n"))
		}
	} else {
		for i := start; i < end; i++ {
			p := visible[i]
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

			line := fmt.Sprintf("%s %s%s PID %-6d %-18s", cursor, marker, portLabel, p.PID, shortName(p))
			if m.showPath {
				line += "  " + shortCmdline(p)
			}
			if m.showInfo {
				line += detailsSuffix(p)
			}
			if m.width > 0 {
				line = ansi.Truncate(line, m.width, "…")
			}
			b.WriteString(line + "\n")
		}
		if start > 0 || end < len(visible) {
			b.WriteString(helpStyle.Render(fmt.Sprintf("  showing %d-%d of %d\n", start+1, end, len(visible))))
		}
	}

	b.WriteString("\n")

	switch m.mode {
	case modeCustomInput:
		b.WriteString("Custom port: " + m.textInput.View() + "\n")
		b.WriteString(helpStyle.Render("Enter confirms. Esc goes back.\n"))
	case modeFilter:
		b.WriteString("Filter: " + m.textInput.View() + "\n")
		b.WriteString(helpStyle.Render("Enter applies. Esc clears and goes back.\n"))
	case modeConfirm:
		if m.killing {
			b.WriteString(fmt.Sprintf("Killing port %d (PID %d)…\n", m.selected.Port, m.selected.PID))
		} else if m.selected != nil {
			b.WriteString(fmt.Sprintf("Kill port %d (PID %d)? Press y to confirm, n to cancel.\n", m.selected.Port, m.selected.PID))
		} else {
			b.WriteString("Nothing selected.\n")
		}
	default:
		b.WriteString(helpStyle.Render("↑/↓: move  enter: kill  /: filter  a: port  p: path  i: info  s: sort  q: quit\n"))
	}

	if m.mode == modeBrowse && m.cursor < len(visible) && visible[m.cursor].Protected {
		b.WriteString("\n" + warnStyle.Render(protectedNotice(visible[m.cursor])) + "\n")
	}
	if m.status != "" {
		b.WriteString(m.status + "\n")
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
	killPortStr := flag.String("kill", "", "Kill a specific listening port or range (e.g. --kill=3000 or --kill=3000-3010)")
	killName := flag.String("kill-name", "", "Kill all listening ports owned by a process name (e.g. --kill-name=node.exe)")
	yes := flag.Bool("yes", false, "Bypass confirmation prompts for non-interactive CLI kills")
	// Short form: -y
	flag.BoolVar(yes, "y", false, "Bypass confirmation prompts for non-interactive CLI kills")
	dryRun := flag.Bool("dry-run", false, "Show what would be killed without killing anything")
	jsonOut := flag.Bool("json", false, "Print the port list as JSON and exit")
	csvOut := flag.Bool("csv", false, "Print the port list as CSV and exit")
	sortKey := flag.String("sort", "port", "Sort order for listings: port, pid, or name")
	showVersion := flag.Bool("version", false, "Print version and exit")
	help := flag.Bool("help", false, "Show help")
	// Kept for backwards compatibility: the TUI is already the default view.
	flag.Bool("interactive", false, "Open the interactive Bubble Tea UI (default; kept for compatibility)")
	flag.Parse()

	if *help {
		printHelp()
		os.Exit(0)
	}
	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}
	switch strings.ToLower(strings.TrimSpace(*sortKey)) {
	case "", "port", "pid", "name":
	default:
		color.Red("Invalid sort %q: use port, pid, or name", *sortKey)
		os.Exit(1)
	}
	if *jsonOut && *csvOut {
		color.Red("Use either --json or --csv, not both.")
		os.Exit(1)
	}

	if *jsonOut || *csvOut {
		ports, err := scanListeningPorts()
		if err != nil {
			color.Red("Error: %s", err)
			os.Exit(1)
		}
		sortPorts(ports, *sortKey)
		if *jsonOut {
			printJSON(ports)
		} else {
			printCSV(ports)
		}
		return
	}

	if *killName != "" {
		ports, err := scanListeningPorts()
		if err != nil {
			color.Red("Error: %s", err)
			os.Exit(1)
		}
		killByName(*killName, ports, *yes, *dryRun)
		return
	}

	if *killPortStr != "" {
		portNums, err := parsePortSpec(*killPortStr)
		if err != nil {
			color.Red("Invalid port: %v", err)
			os.Exit(1)
		}
		killPortNumbers(portNums, *yes, *dryRun)
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

		sortPorts(ports, *sortKey)
		printPorts(ports)
		if *dryRun || *yes || readYesNo("Kill ALL unprotected developer ports") {
			if killPorts(ports, *dryRun) > 0 {
				os.Exit(1)
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

func printHelp() {
	fmt.Println("gkport - Developer port lister & killer")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  gkport")
	fmt.Println("    [--interactive]")
	fmt.Println("    [--kill=PORT|PORT-PORT]")
	fmt.Println("    [--kill-name=NAME]")
	fmt.Println("    [--kill-all]")
	fmt.Println("    [--json | --csv]")
	fmt.Println("    [--sort=port|pid|name]")
	fmt.Println("    [--dry-run]")
	fmt.Println("    [-y|--yes]")
	fmt.Println("    [--version]")
	fmt.Println("    [--help]")
	fmt.Println("")
	fmt.Println("Default view lists every listening TCP port and refuses to kill OS/system ports")
	fmt.Println("(privileged ports, known Windows service ports, and processes owned by SYSTEM).")
	fmt.Println("--kill accepts a single port or an inclusive range like 3000-3010.")
	fmt.Println("--kill-name kills every port owned by the exact process name given (e.g. node.exe).")
	fmt.Println("--kill-all only targets the common developer ports.")
	fmt.Println("--dry-run prints the kill plan without executing it.")
	fmt.Println("Use -y/--yes to bypass confirmation prompts for non-interactive CLI kills.")
	fmt.Println("")
	fmt.Printf("Version: %s\n", version)
}

func readYesNo(prompt string) bool {
	color.Yellow("%s? (y/N): ", prompt)
	var confirm string
	fmt.Scanln(&confirm)
	return strings.EqualFold(strings.TrimSpace(confirm), "y")
}

// killPortNumbers resolves each requested port and kills the unprotected ones,
// refusing protected ports loudly. Exits 1 when nothing ended up killable.
func killPortNumbers(portNums []int, autoYes, dryRun bool) {
	var targets []PortInfo
	for _, portNum := range portNums {
		info, err := findListeningPort(portNum)
		if err != nil {
			color.Red("Error: %v", err)
			os.Exit(1)
		}
		if info == nil {
			color.Yellow("Port %d is not currently listening.", portNum)
			continue
		}
		if info.Protected {
			color.Red("⚠ Refusing to kill port %d: %s.", info.Port, info.ProtectReason)
			continue
		}
		targets = append(targets, *info)
	}
	if len(targets) == 0 {
		os.Exit(1)
	}

	printPorts(targets)
	if !autoYes && !dryRun && !readYesNo(fmt.Sprintf("Kill %d port(s)", len(targets))) {
		return
	}
	if killPorts(targets, dryRun) > 0 {
		os.Exit(1)
	}
}

// killByName kills every listening port whose owning process name matches
// exactly (case-insensitive); protected matches are skipped loudly.
func killByName(name string, ports []PortInfo, autoYes, dryRun bool) {
	name = strings.TrimSpace(name)
	var matched []PortInfo
	for _, p := range ports {
		if strings.EqualFold(strings.TrimSpace(p.Name), name) {
			matched = append(matched, p)
		}
	}
	if len(matched) == 0 {
		color.Yellow("No listening port is owned by a process named %q.", name)
		os.Exit(1)
	}

	printPorts(matched)
	if !autoYes && !dryRun && !readYesNo(fmt.Sprintf("Kill %d port(s) owned by %q", len(matched), name)) {
		return
	}
	if killPorts(matched, dryRun) > 0 {
		os.Exit(1)
	}
}
