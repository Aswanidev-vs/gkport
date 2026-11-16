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

	"github.com/charmbracelet/bubbles/spinner"
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
var version = "v0.4.2"

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

// Palette. A cool graphite ramp tinted toward the socket table it renders, with
// a single teal accent for the row you can act on and ochre reserved for "may not
// kill". Protected rows recede to the dim step instead of shouting in red: the
// eye should land on what you can terminate, not on what you cannot.
var (
	cText   = lipgloss.AdaptiveColor{Light: "#23282E", Dark: "#D3D8DE"}
	cDim    = lipgloss.AdaptiveColor{Light: "#5A636D", Dark: "#9AA3AC"}
	cFaint  = lipgloss.AdaptiveColor{Light: "#69727C", Dark: "#79818A"}
	cAccent = lipgloss.AdaptiveColor{Light: "#16695E", Dark: "#5FB3A4"}
	cNotice = lipgloss.AdaptiveColor{Light: "#8A6A33", Dark: "#C09A5B"}
	cDanger = lipgloss.AdaptiveColor{Light: "#A93B26", Dark: "#E0785F"}
	cRule   = lipgloss.AdaptiveColor{Light: "#D2D8DE", Dark: "#2E343B"}
	cSelBg  = lipgloss.AdaptiveColor{Light: "#E6EAEE", Dark: "#171C22"}
)

var (
	titleStyle  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	textStyle   = lipgloss.NewStyle().Foreground(cText)
	dimStyle    = lipgloss.NewStyle().Foreground(cDim)
	faintStyle  = lipgloss.NewStyle().Foreground(cFaint)
	accentStyle = lipgloss.NewStyle().Foreground(cAccent)
	noticeStyle = lipgloss.NewStyle().Foreground(cNotice)
	dangerStyle = lipgloss.NewStyle().Foreground(cDanger)
	ruleStyle   = lipgloss.NewStyle().Foreground(cRule)
	selRowStyle = lipgloss.NewStyle().Foreground(cText).Background(cSelBg)
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

// portsMsg carries a rescan off the UI thread. Scanning every TCP connection and
// resolving each owner costs long enough to drop frames if it runs inside Update.
type portsMsg struct {
	ports []PortInfo
	err   error
}

type model struct {
	ports     []PortInfo
	cursor    int
	height    int
	width     int
	mode      appMode
	textInput textinput.Model
	spinner   spinner.Model
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

// scanPortsCmd runs a full rescan off the UI thread and reports it through
// portsMsg, so a kill does not freeze the frame that confirms it.
func scanPortsCmd(sortKey string) tea.Cmd {
	return func() tea.Msg {
		ports, err := scanListeningPorts()
		if err != nil {
			return portsMsg{err: err}
		}
		sortPorts(ports, sortKey)
		return portsMsg{ports: ports}
	}
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

// portStillListening reports whether a TCP socket is still in LISTEN on portNum.
// It reads only the connection table: the full rescan that also resolves every
// owner process is far too heavy to run as a poll.
func portStillListening(portNum int) bool {
	conns, err := net.Connections("tcp")
	if err != nil {
		return true
	}
	for _, conn := range conns {
		if conn.Status == "LISTEN" && int(conn.Laddr.Port) == portNum {
			return true
		}
	}
	return false
}

// waitPortGone blocks until the port leaves the TCP table (or the timeout
// expires). Windows releases a terminated process's sockets asynchronously,
// so an immediate rescan would still find the dead owner's LISTEN entry with
// an unresolvable name and render it as "(unknown)". The delay backs off because
// the socket is almost always gone within the first poll or two.
func waitPortGone(portNum int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for delay := 50 * time.Millisecond; ; delay *= 2 {
		if !portStillListening(portNum) || !time.Now().Before(deadline) {
			return
		}
		time.Sleep(min(delay, 250*time.Millisecond))
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
	return fmt.Sprintf("Port %d is protected — %s", p.Port, p.ProtectReason)
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
	runes := []rune(cmdline)
	if len(runes) > 70 {
		return string(runes[:67]) + "..."
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

// detailsSuffix renders the process facts appended by the "i" toggle.
func detailsSuffix(p PortInfo) string {
	parts := make([]string, 0, 3)
	if p.CreateTime > 0 {
		parts = append(parts, "up "+humanDur(time.Since(time.UnixMilli(p.CreateTime))))
	}
	if p.MemRSS > 0 {
		parts = append(parts, fmt.Sprintf("%.1fMB", float64(p.MemRSS)/(1024*1024)))
	}
	if p.CPUPercent > 0 {
		parts = append(parts, fmt.Sprintf("%.1f%% cpu", p.CPUPercent))
	}
	return strings.Join(parts, ", ")
}

func newModel(ports []PortInfo, sortKey string) model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "port, process, command or user"
	ti.CharLimit = 64
	ti.PlaceholderStyle = faintStyle
	ti.TextStyle = textStyle
	ti.Cursor.Style = accentStyle
	ti.Cursor.TextStyle = accentStyle

	if strings.TrimSpace(sortKey) == "" {
		sortKey = "port"
	}

	return model{
		ports:     ports,
		textInput: ti,
		spinner:   spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		sortKey:   sortKey,
	}
}

// chromeLines counts the fixed lines View draws around the list: title, rule,
// column header, protection notice, rule, status and hints. Keeping this constant
// is what stops the footer from shifting the list around as you change modes.
const chromeLines = 7

func (m model) visibleRows() int {
	if m.height <= 0 {
		return 15
	}
	return max(m.height-chromeLines, 3)
}

// Column widths shared by the column header and every row so they cannot drift.
const (
	markerW = 2
	portW   = 5
	pidW    = 6
	nameW   = 20
)

func padLeft(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return ansi.Truncate(s, w, "")
}

func padRight(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return ansi.Truncate(s, w, "")
}

func rule(width int) string {
	if width <= 0 {
		width = 40
	}
	return ruleStyle.Render(strings.Repeat("─", width))
}

// markerCell keeps the marker column exactly two cells wide even in terminals that
// disagree about the glyph's width.
func markerCell(glyph string, style lipgloss.Style) string {
	s := style.Render(glyph)
	return s + strings.Repeat(" ", max(0, markerW-lipgloss.Width(s)))
}

func (m model) columnHeader() string {
	label := func(name, key string) string {
		if m.sortKey == key {
			return textStyle.Render(name)
		}
		return faintStyle.Render(name)
	}
	return "   " +
		padLeft(label("PORT", "port"), portW) + " " +
		padLeft(label("PID", "pid"), pidW) + "  " +
		padRight(label("PROCESS", "name"), nameW) + "  " +
		label("USER", "")
}

func (m model) renderRow(p PortInfo, selected bool) string {
	port := fmt.Sprintf("%*d", portW, p.Port)
	pid := fmt.Sprintf("%*d", pidW, p.PID)
	name := padRight(shortName(p), nameW)

	extra := ""
	if m.showPath {
		extra += "  " + shortCmdline(p)
	}
	if suffix := detailsSuffix(p); m.showInfo && suffix != "" {
		extra += "  " + suffix
	}

	// A selected row is one unbroken run. lipgloss closes every styled span with a
	// reset, so a background wrapped around per-column styles would not survive
	// them — the bar only holds if the whole row is a single style.
	if selected {
		line := "> " + port + " " + pid + "  " + name + "  " + p.User + extra
		if m.width > 0 {
			line = padRight(ansi.Truncate(line, m.width, "…"), m.width)
		}
		return selRowStyle.Render(line)
	}

	marker, base, portColumn := "  ", textStyle, accentStyle
	if p.Protected {
		marker, base, portColumn = markerCell("⚠", noticeStyle), dimStyle, dimStyle
	}

	line := marker + " " +
		portColumn.Render(port) + " " +
		dimStyle.Render(pid) + "  " +
		base.Render(name) + "  " +
		base.Render(p.User)
	if extra != "" {
		line += dimStyle.Render(extra)
	}
	if m.width > 0 {
		line = ansi.Truncate(line, m.width, "…")
	}
	return line
}

func (m model) emptyText() string {
	if m.filter != "" {
		return fmt.Sprintf("nothing matches %q", m.filter)
	}
	return "nothing is listening on TCP"
}

func (m model) statusLine() string {
	switch m.mode {
	case modeCustomInput:
		return accentStyle.Render("port") + "  " + m.textInput.View()
	case modeFilter:
		return accentStyle.Render("filter") + "  " + m.textInput.View()
	case modeConfirm:
		if m.killing {
			return m.spinner.View() + "  " + dimStyle.Render(fmt.Sprintf("killing port %d (pid %d)…", m.selected.Port, m.selected.PID))
		}
		if m.selected == nil {
			return dimStyle.Render("nothing selected")
		}
		return dangerStyle.Render(fmt.Sprintf("kill port %d", m.selected.Port)) +
			dimStyle.Render(fmt.Sprintf("   pid %d", m.selected.PID))
	}
	if m.status != "" {
		return dimStyle.Render(m.status)
	}
	visible := m.visiblePorts()
	if len(visible) == 0 {
		return ""
	}
	return faintStyle.Render(fmt.Sprintf("%d / %d", m.cursor+1, len(visible)))
}

func (m model) protectionLine() string {
	if m.mode != modeBrowse {
		return ""
	}
	visible := m.visiblePorts()
	if m.cursor >= len(visible) || !visible[m.cursor].Protected {
		return ""
	}
	return noticeStyle.Render(protectedNotice(visible[m.cursor]))
}

func hintLine(mode appMode, filtering bool) string {
	type hint struct{ key, label string }
	var hints []hint
	switch mode {
	case modeCustomInput:
		hints = []hint{{"enter", "lookup"}, {"esc", "back"}}
	case modeFilter:
		hints = []hint{{"enter", "apply"}, {"esc", "clear"}}
	case modeConfirm:
		hints = []hint{{"y", "confirm"}, {"any other", "cancel"}}
	default:
		hints = []hint{
			{"↑↓", "move"}, {"↵", "kill"}, {"/", "filter"}, {"a", "port"},
			{"p", "path"}, {"i", "info"}, {"s", "sort"}, {"q", "quit"},
		}
		if filtering {
			hints = append(hints, hint{"esc", "clear filter"})
		}
	}
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, dimStyle.Render(h.key)+" "+faintStyle.Render(h.label))
	}
	return strings.Join(parts, "   ")
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
			strings.Contains(strconv.Itoa(int(p.PID)), f) ||
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
	m.status = "sorted by " + m.sortKey
	m.clampCursor()
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
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
	case tea.MouseMsg:
		// Only the wheel is bound; without it a mouse-driven terminal gives no
		// way to scroll the list at all.
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		if m.mode == modeConfirm || m.mode == modeFilter || m.mode == modeCustomInput {
			return m, nil
		}
		visible := m.visiblePorts()
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.cursor = max(0, m.cursor-1)
		case tea.MouseButtonWheelDown:
			m.cursor = max(0, min(len(visible)-1, m.cursor+1))
		default:
			return m, nil
		}
		m.status = ""
		return m, nil
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		m.textInput.Width = max(20, m.width-4)
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case killResultMsg:
		m.killing = false
		m.mode = modeBrowse
		m.selected = nil
		if msg.err != nil {
			m.status = "Error: " + errText(msg.err)
			return m, nil
		}
		m.status = msg.message
		return m, scanPortsCmd(m.sortKey)
	case portsMsg:
		if msg.err != nil {
			m.status += " (rescan failed: " + msg.err.Error() + ")"
			return m, nil
		}
		m.ports = msg.ports
		m.clampCursor()
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
			m.status = ""
		}
	case "down", "j":
		if m.cursor < len(visible)-1 {
			m.cursor++
			m.status = ""
		}
	case "home", "g":
		m.cursor = 0
		m.status = ""
	case "end", "G":
		m.cursor = max(0, len(visible)-1)
		m.status = ""
	case "pgup":
		m.cursor = max(0, m.cursor-m.visibleRows())
		m.status = ""
	case "pgdown":
		m.cursor = max(0, min(len(visible)-1, m.cursor+m.visibleRows()))
		m.status = ""
	case "p":
		m.showPath = !m.showPath
		m.status = "command line " + onOff(m.showPath)
		return m, nil
	case "i":
		m.showInfo = !m.showInfo
		m.status = "process details " + onOff(m.showInfo)
		return m, nil
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.clampCursor()
			m.status = "Filter cleared."
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
		return m, textinput.Blink
	case "a":
		m.mode = modeCustomInput
		m.textInput.SetValue("")
		m.textInput.Placeholder = "Enter any listening port, e.g. 5173"
		m.textInput.Focus()
		return m, textinput.Blink
	case "enter":
		if len(visible) == 0 {
			m.status = m.emptyText()
			return m, nil
		}
		selected := visible[m.cursor]
		if selected.Protected {
			return m, nil
		}
		m.selected = &selected
		m.mode = modeConfirm
		m.status = ""
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
		m.status = ""
		return m, nil
	case "enter":
		m.mode = modeBrowse
		m.textInput.Blur()
		m.status = ""
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
		m.status = ""
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.textInput.Value())
		portNum, err := strconv.Atoi(value)
		if err != nil || portNum <= 0 || portNum > 65535 {
			m.status = "enter a port between 1 and 65535"
			return m, nil
		}

		info, err := findListeningPort(portNum)
		if err != nil {
			m.status = "Error: " + err.Error()
			return m, nil
		}
		if info == nil {
			m.status = fmt.Sprintf("port %d is not listening", portNum)
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
		m.status = ""
		return m, nil
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.killing {
		return m, nil
	}
	if strings.ToLower(msg.String()) == "y" {
		m.killing = true
		portNum := m.selected.Port
		return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
			info, err := killPortByNumber(portNum)
			if err != nil {
				return killResultMsg{err: err, port: portNum}
			}
			return killResultMsg{message: fmt.Sprintf("killed port %d (pid %d)", info.Port, info.PID), port: info.Port}
		})
	}
	// Any other key cancels; a stray keypress must never fire a kill.
	m.mode = modeBrowse
	m.selected = nil
	m.status = "kill cancelled"
	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	if m.width > 0 && (m.width < 40 || m.height < 10) {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			noticeStyle.Render("Terminal too small — resize to at least 40×10"))
	}

	visible := m.visiblePorts()
	rows := m.visibleRows()

	// The list region always occupies exactly `rows` lines, so entering filter or
	// confirm mode never nudges the content under the cursor.
	list := make([]string, 0, rows)
	if len(visible) == 0 {
		list = append(list, dimStyle.Render("  "+m.emptyText()))
	} else {
		start := max(0, m.cursor-rows+1)
		end := min(len(visible), start+rows)
		for i := start; i < end; i++ {
			active := i == m.cursor
			if m.mode == modeConfirm {
				active = m.selected != nil && visible[i].Port == m.selected.Port
			}
			list = append(list, m.renderRow(visible[i], active))
		}
	}
	for len(list) < rows {
		list = append(list, "")
	}

	hints := hintLine(m.mode, m.filter != "")

	lines := make([]string, 0, chromeLines+rows)
	lines = append(lines,
		m.titleLine(visible),
		rule(m.width),
		m.columnHeader(),
	)
	lines = append(lines, list...)
	lines = append(lines, m.protectionLine(), rule(m.width), m.statusLine(), hints)

	return strings.Join(lines, "\n")
}

func (m model) titleLine(visible []PortInfo) string {
	left := titleStyle.Render("gkport") + faintStyle.Render(" "+version)

	summary := fmt.Sprintf("%d killable", len(visible)-countProtected(visible))
	if m.filter != "" {
		summary = fmt.Sprintf("%d of %d", len(visible), len(m.ports))
	}
	right := dimStyle.Render(summary)
	if protected := countProtected(visible); protected > 0 {
		right += faintStyle.Render("   ") + noticeStyle.Render(fmt.Sprintf("%d protected", protected))
	}

	gap := 1
	if m.width > 0 {
		gap = max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	}
	return left + strings.Repeat(" ", gap) + right
}

func runInteractive(ports []PortInfo, sortKey string) error {
	p := tea.NewProgram(newModel(ports, sortKey), tea.WithAltScreen(), tea.WithMouseCellMotion())
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

	sortPorts(ports, *sortKey)
	if err := runInteractive(ports, *sortKey); err != nil {
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
