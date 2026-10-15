package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestProtectReason(t *testing.T) {
	own := int32(os.Getpid())
	tests := []struct {
		name string
		info PortInfo
		want string // non-empty means protected
	}{
		{"no visible pid", PortInfo{Port: 3000, PID: 0}, "owner PID is not visible"},
		{"own pid", PortInfo{Port: 3000, PID: own}, "belongs to gkport"},
		{"privileged port", PortInfo{Port: 80, PID: 4242}, "privileged port"},
		{"known service port", PortInfo{Port: 3389, PID: 4242, Name: "TermService"}, "Remote Desktop"},
		{"unreadable service process", PortInfo{Port: 3000, PID: 4242, Name: ""}, "Windows service"},
		{"system process", PortInfo{Port: 3000, PID: 4242, Name: "svchost.exe"}, "Windows service host"},
		{"system user", PortInfo{Port: 3000, PID: 4242, Name: "node.exe", User: "SYSTEM"}, "SYSTEM account"},
		{"safe dev server", PortInfo{Port: 3000, PID: 4242, Name: "node.exe", User: "dev"}, ""},
	}
	for _, tt := range tests {
		got := protectReason(tt.info)
		if tt.want == "" {
			if got != "" {
				t.Errorf("%s: expected safe, got protected: %q", tt.name, got)
			}
			continue
		}
		if !strings.Contains(got, tt.want) {
			t.Errorf("%s: expected reason containing %q, got %q", tt.name, tt.want, got)
		}
	}
}

func TestParsePortSpec(t *testing.T) {
	tests := []struct {
		spec    string
		want    []int
		wantErr bool
	}{
		{"3000", []int{3000}, false},
		{"3000-3003", []int{3000, 3001, 3002, 3003}, false},
		{" 5173 ", []int{5173}, false},
		{"", nil, true},
		{"0", nil, true},
		{"70000", nil, true},
		{"abc", nil, true},
		{"3000-2000", nil, true},
		{"3000-", nil, true},
		{"1-1002", nil, true},
	}
	for _, tt := range tests {
		got, err := parsePortSpec(tt.spec)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parsePortSpec(%q): expected error, got %v", tt.spec, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePortSpec(%q): unexpected error: %v", tt.spec, err)
			continue
		}
		if len(got) != len(tt.want) {
			t.Errorf("parsePortSpec(%q) = %v, want %v", tt.spec, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parsePortSpec(%q) = %v, want %v", tt.spec, got, tt.want)
				break
			}
		}
	}
}

func TestSortPorts(t *testing.T) {
	ports := []PortInfo{
		{Port: 8080, PID: 300, Name: "code.exe"},
		{Port: 3000, PID: 100, Name: "node.exe"},
		{Port: 135, PID: 200, Name: ""},
	}
	sortPorts(ports, "port")
	if ports[0].Port != 135 || ports[1].Port != 3000 || ports[2].Port != 8080 {
		t.Errorf("sort by port = %v", ports)
	}
	sortPorts(ports, "pid")
	if ports[0].PID != 100 || ports[1].PID != 200 || ports[2].PID != 300 {
		t.Errorf("sort by pid = %v", ports)
	}
	sortPorts(ports, "name")
	// "(unknown)" sorts first, then code.exe, then node.exe.
	if ports[0].Name != "" || ports[1].Name != "code.exe" || ports[2].Name != "node.exe" {
		t.Errorf("sort by name = %v", ports)
	}
}

func TestVisiblePortsFilter(t *testing.T) {
	m := model{ports: []PortInfo{
		{Port: 3000, PID: 100, Name: "node.exe", Cmdline: "node vite", User: "dev"},
		{Port: 5173, PID: 100, Name: "node.exe", Cmdline: "node electron", User: "dev"},
		{Port: 135, PID: 200, Name: "svchost.exe", User: "SYSTEM"},
	}}
	tests := []struct {
		filter string
		want   int
	}{
		{"", 3},
		{"node", 2},
		{"5173", 1},
		{"NODE", 2},
		{"electron", 1},
		{"system", 1},
		{"nothingmatches", 0},
	}
	for _, tt := range tests {
		m.filter = tt.filter
		if got := len(m.visiblePorts()); got != tt.want {
			t.Errorf("filter %q: got %d ports, want %d", tt.filter, got, tt.want)
		}
	}
	m.filter = ""
	if got := len(m.visiblePorts()); got != 3 {
		t.Errorf("cleared filter: got %d ports, want 3", got)
	}
}

func TestCycleSort(t *testing.T) {
	m := &model{
		ports:   []PortInfo{{Port: 3000, PID: 300}, {Port: 135, PID: 100}},
		sortKey: "port",
	}
	m.cycleSort()
	if m.sortKey != "pid" {
		t.Errorf("after first cycle: sortKey = %q, want pid", m.sortKey)
	}
	if m.ports[0].PID != 100 {
		t.Errorf("cycleSort did not re-sort: %v", m.ports)
	}
	m.cycleSort()
	if m.sortKey != "name" {
		t.Errorf("after second cycle: sortKey = %q, want name", m.sortKey)
	}
	m.cycleSort()
	if m.sortKey != "port" {
		t.Errorf("after third cycle: sortKey = %q, want port", m.sortKey)
	}
}

func TestClampCursor(t *testing.T) {
	m := &model{ports: []PortInfo{{Port: 1}, {Port: 2}, {Port: 3}}}
	m.cursor = 10
	m.clampCursor()
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.cursor)
	}
	m.filter = "zzz"
	m.clampCursor()
	if m.cursor != 0 {
		t.Errorf("cursor with empty visible = %d, want 0", m.cursor)
	}
}

func TestShortNameAndCmdline(t *testing.T) {
	if got := shortName(PortInfo{Name: ""}); got != "(unknown)" {
		t.Errorf("shortName(empty) = %q", got)
	}
	if got := shortName(PortInfo{Name: "  "}); got != "(unknown)" {
		t.Errorf("shortName(blank) = %q", got)
	}
	if got := shortName(PortInfo{Name: "node.exe"}); got != "node.exe" {
		t.Errorf("shortName = %q", got)
	}
	if got := shortCmdline(PortInfo{}); got != "(command unavailable)" {
		t.Errorf("shortCmdline(empty) = %q", got)
	}
	long := strings.Repeat("x", 100)
	if got := shortCmdline(PortInfo{Cmdline: long}); len(got) != 70 || !strings.HasSuffix(got, "...") {
		t.Errorf("shortCmdline(long) = %q (len %d)", got, len(got))
	}
}

func TestHumanDur(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
	}
	for _, tt := range tests {
		if got := humanDur(tt.d); got != tt.want {
			t.Errorf("humanDur(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestDetailsSuffix(t *testing.T) {
	if got := detailsSuffix(PortInfo{}); got != "" {
		t.Errorf("detailsSuffix without CreateTime = %q, want empty", got)
	}
	got := detailsSuffix(PortInfo{
		CreateTime: time.Now().Add(-2 * time.Hour).UnixMilli(),
		MemRSS:     150 * 1024 * 1024,
		CPUPercent: 12.34,
	})
	if !strings.Contains(got, "up 2h") || !strings.Contains(got, "150.0MB") || !strings.Contains(got, "12.3% cpu") {
		t.Errorf("detailsSuffix = %q", got)
	}
}

func TestErrText(t *testing.T) {
	if got := errText(errString("Access is denied.")); !strings.Contains(got, "Administrator") {
		t.Errorf("errText(access denied) = %q", got)
	}
	if got := errText(errString("boom")); got != "boom" {
		t.Errorf("errText(other) = %q", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
