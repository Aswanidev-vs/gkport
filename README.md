# GKPort [![Go](https://img.shields.io/badge/Go-1.24.2-brightgreen.svg)](https://golang.org/) [![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)



GKPort is a fast, terminal-based CLI and TUI tool for **Windows developers** to discover and kill common development server ports (like 3000, 8080) or any listening TCP port. Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) for smooth interactive UI.

## ✨ Features
- 🔍 **Auto-scan** common dev ports (3000, 8080, etc.) that are actively **LISTENING**
- 🎮 **Interactive TUI**: Navigate, select, kill with vim-like keys (j/k, Enter)
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
| `gkport.exe` | List common dev ports + launch interactive TUI |
| `gkport.exe --interactive` | Force TUI mode |
| `gkport.exe --kill 3000` | Kill specific listening port (interactive confirm) |
| `gkport.exe --kill-all` | Kill ALL detected common dev ports (confirm) |
| `gkport.exe --help` | Show help |

**Note**: `--kill` works for **any** listening TCP port, not just common ones.

## 🎮 Interactive TUI

Run `gkport.exe` or `gkport.exe --interactive`

```
GKPort
Common developer ports currently listening

> :3000  PID 1234
  :8080  PID 5678
  :5000  PID 9999

Up/Down: move  Enter: kill selected  p: toggle path  a: add custom port  q: quit
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

## 🎯 Default Dev Ports
```
3000, 3001, 3002 • 4000 • 5000, 5001 • 8000, 8001 • 8080, 8081 • 9000 • 9090
```
*(React, Next.js, Express, Flask/Django, Go, etc.)*

## ⚠️ Notes
- **Admin rights** may be needed for system processes
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
