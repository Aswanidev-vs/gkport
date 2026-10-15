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
| Live filter | `/` type-ahead filter by port, process name, command, or user |
| Sorting | Cycle `port` → `pid` → `name` with `s` in the TUI, or `--sort=` on the CLI |
| Process details | `i` (TUI) or JSON output show start time, RSS memory and CPU% per owner |
| Custom ports | Type any port (5173, 4200, …) to look it up on the fly |
| CLI shortcuts | `--kill 3000`, `--kill 3000-3010`, `--kill-name node.exe`, `--kill-all`, and `-y` to skip prompts |
| Machine-readable | `--json` and `--csv` listing output for scripting |
| Dry run | `--dry-run` prints the exact kill plan without executing it |
| Paging keys | `g`/`G`, `Home`/`End`, `PageUp`/`PageDown` |
| Process info | Shows **PID** and **process name** per port, with a togglable **command line** view (`p`) |
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
| `gkport.exe --kill 3000-3010` | Kill an inclusive **range** of ports (asks first, skips protected) |
| `gkport.exe --kill-name node.exe` | Kill every port owned by a process name (asks first, skips protected) |
| `gkport.exe --kill-all` | Kill all detected common dev ports (asks first, skips protected) |
| `gkport.exe --kill-all -y` | Same, but skip the confirmation prompt |
| `gkport.exe --dry-run --kill-all` | Print the kill plan without killing anything |
| `gkport.exe --json` | Print the full port list as JSON and exit |
| `gkport.exe --csv` | Print the full port list as CSV and exit |
| `gkport.exe --sort=pid` | Sort any listing by `port` (default), `pid`, or `name` |
| `gkport.exe --version` | Print the build version |
| `gkport.exe --help` | Show help and the build version |
| `gkport.exe --interactive` | Accepted for backwards compatibility — the TUI is already the default |

**Exit codes**: `0` on success (including a dry run), `1` on an invalid port/range,
a port that is not listening, a protected port, a failed kill, or invalid flag
values.

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

A PID that owns several ports is killed once — the extra ports are reported as
a note instead of a spurious failure.

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

### Port ranges and kill-by-name

`--kill` accepts a single port or an inclusive range:

```console
$ gkport.exe --dry-run --kill 3000-3003
Port    PID     Cmdline                          User
3000    9156    node ...vite                     BEASTGAMER\LENOVO
3002    9157    node ...esbuild                  BEASTGAMER\LENOVO
[dry-run] Would kill PID 9156 (port 3000)
[dry-run] Would kill PID 9157 (port 3002)
```

`--kill-name` kills every listening port owned by one process name (exact,
case-insensitive match — never a partial match):

```console
$ gkport.exe --dry-run --kill-name node.exe
[...]
[dry-run] Would kill PID 9156 (port 3000)
```

### Dry run

`--dry-run` works with every kill mode (`--kill`, ranges, `--kill-name`,
`--kill-all`): it prints the exact plan — including which ports it would refuse
— and kills nothing. Exit code is `0`, so it is safe in pipelines:

```console
$ gkport.exe --dry-run --kill-all
...
[dry-run] Would kill PID 14512 (port 5173)
```

### Machine-readable output

`--json` and `--csv` print the full listing (including the `protected` flag and
`protect_reason`) and exit — ideal for scripts and status bars:

```console
$ gkport.exe --json --sort pid
[
  {
    "port": 135,
    "pid": 2148,
    "cmdline": "",
    "user": "",
    "name": "",
    "create_time_ms": 0,
    "cpu_percent": 0,
    "mem_rss_bytes": 0,
    "protected": true,
    "protect_reason": "port 135 is a privileged port reserved for OS services"
  }
]
```

```console
$ gkport.exe --csv --sort name
port,pid,name,user,protected,reason
135,2148,(unknown),,true,port 135 is a privileged port reserved for OS services
```

## Interactive TUI

Run `gkport.exe` (or `gkport.exe --interactive`).

```
GKPort v0.2.2
Listening TCP ports: 31    13 protected and cannot be killed
⚠ = protected: OS/service-owned — gkport will never kill it

> ⚠ :135   PID 2144   svchost.exe
  ⚠ :445   PID 4      System
    :4096  PID 9200   node.exe
    :8787  PID 6504   code.exe

↑/↓: move  enter: kill  /: filter  a: port  p: path  i: info  s: sort  q: quit

⚠ Port 135 is protected: Windows RPC endpoint mapper. gkport will not kill it.
```

The list is a single scrolling window sized to your terminal, with the selected
row marked by `>`. When the selected port is protected, the ⚠ notice appears
above the status line so you always see *why* it is blocked. The header always
shows the `⚠ = protected` legend, and rows are truncated to the terminal width.

Pressing `p` appends the owning command line to each row:

```
>   :3000  PID 14512  node.exe           node C:\dev\my-app\node_modules\.bin\vite
```

Pressing `i` appends process details — uptime, resident memory, and CPU%
(since process start):

```
>   :3000  PID 14512  node.exe  · up 2h · 312.4MB · 4.2% cpu
```

If the terminal is smaller than 40x10, gkport shows a resize notice instead of
a garbled screen.

**Controls**

| Key | Action |
|---|---|
| `Up`/`k`, `Down`/`j` | Move the selection |
| `g`/`Home`, `G`/`End` | Jump to the top / bottom of the list |
| `PageUp`/`PageDown` | Move by a screenful |
| `Enter` | Select the port and ask for confirmation |
| `y` | Confirm the kill (any other key cancels) |
| `/` | Live filter by port, name, command, or user |
| `a` | Enter a custom port number |
| `p` | Toggle the command-line column |
| `i` | Toggle process details (uptime, memory, CPU%) |
| `s` | Cycle sort order: port → pid → name |
| `q` or `Ctrl+C` | Quit (`Ctrl+C` works from every mode) |

**Pro tip**: The `a` prompt looks up **any** listening TCP port system-wide
(handy for `4200`, `8787`, `24000`, …), not just the preset dev list. The `/`
filter matches across all columns, so `vite` or `5173` narrows the list
instantly; `Esc` clears it.

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
- **Real-time**: the TUI rescans automatically after each kill, and waits for
  Windows to release the killed socket first — so you never see a dead owner
  linger in the list as `(unknown)`
- **Windows-only** process info (gopsutil + `taskkill` fallback)
- **Tests**: `go test ./...` covers the safety classifier, port specs, sort,
  filter, and a kill-then-rescan regression test (Windows)
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
 +-- waitPortGone()         poll until Windows releases the killed socket
 +-- killPorts()            loud, never-silent skips + --dry-run support
```

* **All logic lives in one file**, [`main.go`](main.go) (~1050 lines) — no `internal/` packages.
* Ports are de-duplicated by number, so a dual-stack (IPv4 + IPv6) server appears once.
* `--kill-all` reuses the full scan and filters it by `commonDevPorts`, so it can
  never touch a port outside the dev set even if it is unprotected.
* Every kill funnels through `protectReason()` — the TUI, `--kill`, ranges,
  `--kill-name` and `--kill-all` share the same classifier.

## Screenshots

*(Add demo GIF here)*

---

Made with care for devs who hate `netstat | find` hell
