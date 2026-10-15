package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func testModel() model {
	return newModel([]PortInfo{
		{Port: 3000, PID: 100, Name: "node.exe", User: "dev"},
		{Port: 5173, PID: 200, Name: "node.exe", User: "dev"},
		{Port: 135, PID: 300, Name: "svchost.exe", User: "SYSTEM", Protected: true, ProtectReason: "privileged"},
	})
}

func TestCtrlCQuitsFromEveryMode(t *testing.T) {
	for _, mode := range []appMode{modeBrowse, modeCustomInput, modeConfirm, modeFilter} {
		m := testModel()
		m.mode = mode
		updated, _ := m.Update(key("ctrl+c"))
		got := updated.(model)
		if !got.quitting {
			t.Errorf("mode %d: ctrl+c did not quit", mode)
		}
	}
}

func TestConfirmAnyKeyCancels(t *testing.T) {
	m := testModel()
	m.selected = &m.ports[0]
	m.mode = modeConfirm
	updated, _ := m.Update(key("x")) // not "y" -> cancel, never kill
	got := updated.(model)
	if got.mode != modeBrowse || got.selected != nil {
		t.Errorf("unexpected key did not cancel: mode=%d selected=%v", got.mode, got.selected)
	}
	if got.status != "Kill cancelled." {
		t.Errorf("status = %q", got.status)
	}
}

func TestConfirmBlockedWhileKilling(t *testing.T) {
	m := testModel()
	m.selected = &m.ports[0]
	m.mode = modeConfirm
	m.killing = true
	updated, _ := m.updateConfirm(key("y"))
	got := updated.(model)
	// A second "y" must not fire another kill: no new kill cmd, back to
	// browse; killing stays true until the in-flight killResultMsg arrives.
	if got.mode != modeBrowse || got.selected != nil || !got.killing {
		t.Errorf("double-kill guard failed: mode=%d selected=%v killing=%v", got.mode, got.selected, got.killing)
	}
}

func TestFilterFlow(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(key("/"))
	got := updated.(model)
	if got.mode != modeFilter {
		t.Fatalf("'/' did not open filter mode")
	}
	for _, r := range "5173" {
		updated, _ = got.updateFilter(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		got = updated.(model)
	}
	if got.filter != "5173" {
		t.Fatalf("filter = %q", got.filter)
	}
	if len(got.visiblePorts()) != 1 || got.visiblePorts()[0].Port != 5173 {
		t.Errorf("filter did not narrow the list: %v", got.visiblePorts())
	}
	updated, _ = got.updateFilter(key("enter"))
	got = updated.(model)
	if got.mode != modeBrowse || got.filter != "5173" {
		t.Errorf("enter should keep filter: mode=%d filter=%q", got.mode, got.filter)
	}
	updated, _ = got.updateFilter(key("esc")) // esc only clears from filter mode
	got = updated.(model)
	got.filter = ""
	got.clampCursor()
	if len(got.visiblePorts()) != 3 {
		t.Errorf("cleared filter did not restore the list")
	}
}

func TestPagingKeys(t *testing.T) {
	m := testModel()
	m.cursor = 1
	updated, _ := m.updateBrowse(key("G"))
	got := updated.(model)
	if got.cursor != 2 {
		t.Errorf("G: cursor = %d, want 2", got.cursor)
	}
	updated, _ = got.updateBrowse(key("g"))
	got = updated.(model)
	if got.cursor != 0 {
		t.Errorf("g: cursor = %d, want 0", got.cursor)
	}
	updated, _ = got.updateBrowse(key("end"))
	got = updated.(model)
	if got.cursor != 2 {
		t.Errorf("end: cursor = %d, want 2", got.cursor)
	}
	updated, _ = got.updateBrowse(key("home"))
	got = updated.(model)
	if got.cursor != 0 {
		t.Errorf("home: cursor = %d, want 0", got.cursor)
	}
}

func TestEnterOnProtectedDoesNotConfirm(t *testing.T) {
	m := testModel()
	m.cursor = 2 // protected svchost entry
	updated, _ := m.updateBrowse(key("enter"))
	got := updated.(model)
	if got.mode != modeBrowse {
		t.Errorf("protected port opened the kill confirm: mode=%d", got.mode)
	}
	if !strings.Contains(got.status, "protected") {
		t.Errorf("status = %q, want protected notice", got.status)
	}
}

func TestViewShowsLegendAndStatus(t *testing.T) {
	m := testModel()
	m.cursor = 2
	m.status = "Kill cancelled."
	view := m.View()
	for _, want := range []string{"GKPort", "⚠ = protected", "Kill cancelled.", "protected"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestViewTerminalTooSmall(t *testing.T) {
	m := testModel()
	m.width, m.height = 30, 8
	view := m.View()
	if !strings.Contains(view, "Terminal too small") {
		t.Errorf("small-terminal guard not shown:\n%s", view)
	}
}

func TestViewFilterBox(t *testing.T) {
	m := testModel()
	m.mode = modeFilter
	m.filter = "node"
	view := m.View()
	if !strings.Contains(view, "Filter:") || !strings.Contains(view, "filter \"node\"") {
		t.Errorf("filter UI missing:\n%s", view)
	}
}
