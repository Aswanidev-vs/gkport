# GKPort [![Go](https://img.shields.io/badge/Go-1.24.2-brightgreen.svg)](https://golang.org/) [![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)



GKPort is a fast, terminal-based CLI and TUI tool for **Windows developers** to discover and kill common development server ports (like 3000, 8080) or any listening TCP port. Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) for smooth interactive UI.

## ✨ Features
- 🔍 **Auto-scan** every **LISTENING** TCP port on the machine, not just a preset list
- 🛡️ **Protected ports**: privileged ports (below 1024), known Windows service ports, and processes owned by SYSTEM/other OS accounts are flagged with **⚠** and **cannot be killed** from the TUI or CLI
- 🎮 **Interactive TUI**: scrollable list, navigate with j/k or arrows, Enter to select
- ⌨️ **Custom ports**: Enter any port (e.g., 5173, 4200) - works for ALL listening TCP ports
- ⚡ **CLI shortcuts**: `--kill 3000`, `--kill-all`, `--interactive`
- 🪟 **Windows-native**: Uses `taskkill /F` fallback for stubborn processes
- 📊 Shows **PID** for each port, with **togglable path/command line** view (`p` key)

## 🚀 Installation

### Go Install (Recommended)
```bash
go install github.com/Aswanidev-vs/gkport@latest
```
Available globally as `gkport` command.

### Prebuilt Binary
Download `gkport.exe` from [Releases](https://github.com/Aswanidev-vs/gkport/releases)

### From Source
```bash
git clone https://github.com/Aswanidev-vs/gkport.git
cd gkport
go mod tidy
go build -o gkport.exe
```

**Single binary**: No runtime deps! ~15MB executable.

## 📋 Quick Commands

| Command | Description |
|---------|-------------|
| `gkport.exe` | List every listening TCP port + launch interactive TUI |
| `gkport.exe --interactive` | Force TUI mode |
| `gkport.exe --kill 3000` | Kill specific listening port (interactive confirm) |
| `gkport.exe --kill-all` | Kill ALL detected common dev ports (confirm, skips protected) |
| `gkport.exe --help` | Show help |

**Note**: `--kill` works for **any** listening TCP port, but refuses protected system ports.

## 🎮 Interactive TUI

Run `gkport.exe` or `gkport.exe --interactive`

```
GKPort
Listening TCP ports: 31   ⚠ 13 protected and cannot be killed

> ⚠ :135   PID 2144
  ⚠ :445   PID 4
    :4096  PID 9200
    :8787  PID 6504

Up/Down: move  Enter: kill selected  p: toggle path  a: custom port  q: quit

⚠ Port 135 is protected: port 135 is a privileged port reserved for OS services. gkport will not kill it.
```

**Controls**:
| Key | Action |
|-----|--------|
| ↑/↓ or j/k | Navigate ports |
| Enter | Select to kill |
| `p` | Toggle path/command line display |
| `a` | Enter custom port |
| `y` | Confirm kill |
| `n`/Esc | Cancel |
| `q`/Ctrl+C | Quit |

**Pro tip**: Custom input (`a`) scans **all** TCP ports system-wide.

## 🛡️ Protected Ports (⚠)

gkport refuses to kill ports it cannot prove are yours. A port is marked **⚠ protected** when any of these hold:

1. The port is **below 1024** (privileged range reserved for the OS)
2. It is a known **Windows service port** — 135, 137-139, 445, 593, 1900, 2869, 3389 (RDP), 3702, 5040, 5353, 5355, 5357, 5985/5986 (WinRM), 7680
3. The owning process is a **system process** (System, lsass.exe, services.exe, svchost.exe, winlogon.exe, ...)
4. The owner is a **system account** (SYSTEM, LOCAL SERVICE, NETWORK SERVICE, root)
5. The owner is **not readable** — Windows hides session 0 (service) processes from a non-elevated process, so the tool cannot verify the owner and refuses to act
6. The PID belongs to **gkport itself or the shell that launched it**

The list and rules live at the top of `main.go` (`protectedPorts`, `systemProcessNames`, `systemUsers`) if you need to adjust them.

**Running gkport as Administrator** resolves case 5, so service ports are identified by name instead of being blocked as unknown.

## 🎯 Default Dev Ports (`--kill-all` target set)
```
3000, 3001, 3002 • 4000 • 5000, 5001 • 8000, 8001 • 8080, 8081 • 9000 • 9090
```
*(React, Next.js, Express, Flask/Django, Go, etc.)*

## ⚠️ Notes
- **Protected ports are never killed** — the TUI and CLI both refuse them (see above)
- **Admin rights** reveal service-owned ports; without them those ports are treated as protected
- **Safe**: Always confirms before killing
- **Real-time**: Rescans after kills
- Windows-only process info (gopsutil + taskkill fallback)
- MIT License - feel free to fork/extend!

## 🤝 Contributing
1. Fork & PR
2. Add new common ports to `commonDevPorts`
3. `go mod tidy && go build`

## 📸 Screenshots
*(Add demo GIF here)*

---
Made with ❤️ for devs who hate `netstat | find` hell
