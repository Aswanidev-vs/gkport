# GKPort

[![Go](https://img.shields.io/badge/Go-1.24.2-brightgreen.svg)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![CI](https://github.com/Aswanidev-vs/gkport/actions/workflows/ci.yml/badge.svg)](https://github.com/Aswanidev-vs/gkport/actions/workflows/ci.yml)
[![Release](https://github.com/Aswanidev-vs/gkport/actions/workflows/release.yml/badge.svg)](https://github.com/Aswanidev-vs/gkport/releases/latest)

> **A fast, terminal-based CLI + TUI for Windows developers to discover and kill
> development server ports** (3000, 8080, 5173, …) — or **any** listening TCP port.
> Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).

GKPort scans every **LISTENING** TCP socket on the machine, shows you the owning
**PID** and user, and **refuses to touch anything that belongs to Windows**:
privileged ports, known service ports, system processes and SYSTEM-owned
processes are flagged and blocked from both the TUI and the CLI.

## Features

| Feature | Description |
|---|---|
| Auto-scan | Every **LISTENING** TCP port — not just a preset list |
| Protected ports | Privileged (<1024), known Windows service ports, system processes and SYSTEM-owned processes are flagged `⚠` and **cannot be killed** |
| Interactive TUI | Scrollable list, `j`/`k` or arrow keys, live status line |
| Custom ports | Type any port (5173, 4200, …) to look it up on the fly |
| CLI shortcuts | `--kill 3000`, `--kill-all`, and `-y` to skip prompts |
| Process info | Shows **PID** per port, with a togglable **command line** view (`p`) |
| Windows-native | gopsutil -> `proc.Kill()` -> `taskkill /F` fallback |
| Single binary | ~4 MB stripped, no runtime dependencies |
| Fail-safe | An unverifiable port is treated as protected, never killed |

## Installation

### Go Install (recommended)
```bash
go install github.com/Aswanidev-vs/gkport@latest
```
Installs `gkport` into `%GOPATH%\bin` (on your `PATH` by default).

> Requires **Go 1.24.2+** (`go.mod` is pinned to that toolchain version).

### Prebuilt binaries
Grab a binary from [**Releases**](https://github.com/Aswanidev-vs/gkport/releases):

| Download | Platform |
|---|---|
| `gkport-windows-amd64.exe` | Windows 64-bit (most common) |
| `gkport-windows-arm64.exe` | Windows on ARM |
| `gkport.exe` | Always-newest rolling build |

Each release also ships `SHA256SUMS.txt`. To verify a download:

```powershell
Get-FileHash .\gkport-windows-amd64.exe -Algorithm SHA256
```

Compare the output against the matching line in `SHA256SUMS.txt`.

* Use [`releases/latest`](https://github.com/Aswanidev-vs/gkport/releases/tag/latest)
  for a **permanent** `gkport.exe` link.
* Use a **versioned tag** (`v0.2.2`, …) for a pinned, immutable build.

### From source
```bash
git clone https://github.com/Aswanidev-vs/gkport.git
cd gkport
go mod tidy
go build -o gkport.exe
```

To produce a small stripped binary like the releases:

```bash
go build -trimpath -ldflags "-s -w -X main.version=v0.2.3" -o gkport.exe .
```

`main.version` is what `gkport --help` and the TUI header report.
A plain `go build` leaves it as `dev`.

## Quick Commands

| Command | Description |
|---|---|
| `gkport.exe` | List every listening TCP port and open the interactive TUI |
| `gkport.exe --kill 3000` | Kill a specific listening port (asks for confirmation) |
| `gkport.exe --kill 3000 -y` | Same, but skip the confirmation prompt (scripts) |
| `gkport.exe --kill-all` | Kill all detected common dev ports (asks first, skips protected) |
| `gkport.exe --kill-all -y` | Same, but skip the confirmation prompt |
| `gkport.exe --help` | Show help and the build version |
| `gkport.exe --interactive` | Accepted for backwards compatibility — the TUI is already the default |

**Exit codes**: `0` on success, `1` on an invalid port, a port that is not
listening, a protected port, or a failed kill.

**Note**: `--kill` works for **any** listening TCP port, but refuses protected system ports.

`--yes` is the long form of `-y` — either one works.

### Non-interactive / scripting

Both kill modes print a tab-separated table first, then one line per port:

```console
$ gkport.exe --kill-all -y
Developer Ports (LISTENING):
Port    PID     Cmdline                  User
3000    9156    node ...node_modules\.bin\vite    BEASTGAMER\LENOVO
5173    14512   node ...node_modules\.bin\vite    BEASTGAMER\LENOVO
Killed PID 9156 (port 3000)
Killed PID 14512 (port 5173)
```

Protected entries are skipped, and every skip is printed with its reason —
never silently:

```
⚠ Skipped port 3000: it is owned by the SYSTEM account
```

Targeted kills are just as safe:

```console
$ gkport.exe --kill 135 -y
⚠ Refusing to kill port 135: port 135 is a privileged port reserved for OS services.
$ echo $?
1
```

```console
$ gkport.exe --kill 59999 -y
Port 59999 is not currently listening.
$ echo $?
1
```

## Interactive TUI

Run `gkport.exe` (or `gkport.exe --interactive`).

```
GKPort v0.2.2
Listening TCP ports: 31    13 protected and cannot be killed

> ⚠ :135   PID 2144
  ⚠ :445   PID 4
    :4096  PID 9200
    :8787  PID 6504

Up/Down: move  Enter: kill selected  p: toggle path  a: custom port  q: quit

⚠ Port 135 is protected: Windows RPC endpoint mapper. gkport will not kill it.
```

The list is a single scrolling window sized to your terminal, with the selected
row marked by `>`. When the selected port is protected, the ⚠ notice
automatically replaces the status line so you always see *why* it is blocked.

Pressing `p` appends the owning command line to each row:

```
>   :3000  PID 14512  node C:\dev\my-app\node_modules\.bin\vite
```

**Controls**

| Key | Action |
|---|---|
| `Up`/`k`, `Down`/`j` | Move the selection |
| `Enter` | Select the port and ask for confirmation |
| `y` | Confirm the kill |
| `n` or `Esc` | Cancel the confirmation |
| `p` | Toggle the command-line column |
| `a` | Enter a custom port number |
| `q` or `Ctrl+C` | Quit |

**Pro tip**: The `a` prompt looks up **any** listening TCP port system-wide
(handy for `4200`, `8787`, `24000`, …), not just the preset dev list.

## Protected Ports

gkport refuses to kill ports it cannot prove are yours. A port is marked
**⚠ protected** when any of these hold — checked in this order:

1. **No visible owner PID** — Windows hides session-0 (service) processes from a
   non-elevated process, so the owner cannot be verified
2. The PID belongs to **gkport itself or the shell that launched it**
3. The port is **below 1024** (privileged range reserved for the OS)
4. It is a known **Windows service port** — 135, 137-139, 445, 593, 1900, 2869,
   3389 (RDP), 3702, 5040, 5353, 5355, 5357, 5985/5986 (WinRM), 7680
5. The owning process is a **system process** — `System`, `smss.exe`, `csrss.exe`,
   `wininit.exe`, `winlogon.exe`, `services.exe`, `lsass.exe`, `svchost.exe`,
   `dwm.exe`, `spoolsv.exe`, `MsMpEng.exe`, …
6. The owner is a **system account** — SYSTEM, LOCAL SERVICE, NETWORK SERVICE,
   root (recognised by name *or* by raw SID when Windows will not resolve it)

**Running gkport as Administrator** resolves case 1, so service-owned ports show
up with their real process name instead of being blocked as unknown.

The rules and lists live at the top of [`main.go`](main.go) — `protectedPorts`,
`systemProcessNames` and `systemUsers` — if you need to adjust them.

## Default Dev Ports (`--kill-all` target set)
```
3000, 3001, 3002 • 4000 • 5000, 5001 • 8000, 8001 • 8080, 8081 • 9000 • 9090
```
*(React, Next.js, Express, Flask/Django, Go, etc.)*

`--kill-all` scans **all** listening ports and then filters down to this set, so
it only ever touches your dev servers. Protected entries are skipped, and every
skip is printed with its reason:

```
⚠ Skipped port 3000: it is owned by the SYSTEM account
Killed PID 14512 (port 5173)
```

## Notes
- **Protected ports are never killed** — the TUI and CLI both refuse them
- **Admin rights** reveal service-owned ports; without them those ports are
  treated as protected (fail-safe, never fail-open)
- **Safe by default**: every kill asks for confirmation unless you pass `-y`
- **Real-time**: the TUI rescans automatically after each kill
- **Windows-only** process info (gopsutil + `taskkill` fallback)
- MIT License — feel free to fork/extend!

## Contributing
1. Fork & PR
2. Add new common ports to `commonDevPorts` in [`main.go`](main.go)
3. `go mod tidy && go vet ./... && go build`

Every push and PR runs the [CI workflow](.github/workflows/ci.yml): `gofmt`,
`go vet`, `go test`, a `go mod tidy` check, and a cross-compile for Windows
`amd64` + `arm64`. Please make sure `gofmt -l .` is clean before opening a PR.

## Releasing (maintainers)

Releases are fully automated by [`.github/workflows/release.yml`](.github/workflows/release.yml).
Pushing a semver tag is all that is needed:

```bash
git checkout main
git pull
git tag v0.3.0
git push origin v0.3.0
```

The workflow then:

1. Resolves and validates the tag (must start with `v`)
2. Runs `go mod tidy` check, `go vet`, and `go test`
3. Cross-compiles `gkport-windows-amd64.exe` and `gkport-windows-arm64.exe` with the tag
   injected into `main.version` (visible via `gkport --help` and in the TUI header)
4. Generates `SHA256SUMS.txt`
5. Publishes the GitHub Release with auto-generated release notes
6. Refreshes the rolling [`latest` release](https://github.com/Aswanidev-vs/gkport/releases/tag/latest)

Tags containing a hyphen (e.g. `v0.3.0-rc1`) are automatically marked as pre-releases.
Use the workflow's **Run workflow** button with a `tag` input to rebuild an existing tag,
or with no input to build from the current branch.

## How it works

```
gkport
 +-- scanListeningPorts()   gopsutil/net Connections("tcp") -> keep LISTEN
 +-- protectReason()        classify each port as safe or protected
 +-- killPID()              gopsutil/Kill -> taskkill /F fallback
```

* **All logic lives in one file**, [`main.go`](main.go) (~700 lines) — no `internal/` packages.
* Ports are de-duplicated by number, so a dual-stack (IPv4 + IPv6) server appears once.
* `--kill-all` reuses the full scan and filters it by `commonDevPorts`, so it can
  never touch a port outside the dev set even if it is unprotected.

## Screenshots

*(Add demo GIF here)*

---

Made with care for devs who hate `netstat | find` hell
