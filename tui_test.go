package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
	}, "port")
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
	if got.status != "kill cancelled" {
		t.Errorf("status = %q", got.status)
	}
}

func TestConfirmBlockedWhileKilling(t *testing.T) {
	m := testModel()
	m.selected = &m.ports[0]
	m.mode = modeConfirm
	m.killing = true
	updated, cmd := m.updateConfirm(key("y"))
	got := updated.(model)
	// While a kill is in flight all keys are locked: no second kill, no cancel,
	// state stays in confirm mode until killResultMsg arrives.
	if got.mode != modeConfirm || got.selected == nil || !got.killing {
		t.Errorf("kill lock failed: mode=%d selected=%v killing=%v", got.mode, got.selected, got.killing)
	}
	if cmd != nil {
		t.Errorf("locked key fired a command")
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
	if !strings.Contains(got.protectionLine(), "protected") {
		t.Errorf("protection line = %q, want a protected notice", got.protectionLine())
	}
}

func TestViewRendersHeaderColumnsAndStatus(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 24
	m.cursor = 2
	m.status = "kill cancelled"
	view := m.View()
	for _, want := range []string{"gkport", "PORT", "PROCESS", "USER", "2 killable", "1 protected", "Port 135 is protected", "kill cancelled"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

// The list region is padded to a fixed number of lines so switching modes cannot
// shift the row under the cursor.
func TestViewHeightIsStableAcrossModes(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 24

	m.mode = modeFilter
	filtered := m.View()

	m.mode = modeConfirm
	m.selected = &m.ports[0]
	confirming := m.View()

	m.mode = modeBrowse
	browsing := m.View()

	for name, view := range map[string]string{"browse": browsing, "filter": filtered, "confirm": confirming} {
		if got := strings.Count(view, "\n") + 1; got != 24 {
			t.Errorf("%s: rendered %d lines, want 24\n%s", name, got, view)
		}
	}
}

func TestColumnsLineUpWithHeader(t *testing.T) {
	m := testModel()
	header := ansi.Strip(m.columnHeader())
	row := ansi.Strip(m.renderRow(m.ports[0], false))
	if got, want := strings.Index(header, "PID"), strings.Index(row, "100"); got != want {
		t.Errorf("PID column at %d in the header but %d in the row\nheader: %s\nrow:    %s", got, want, header, row)
	}
}

func TestWheelScrollsTheList(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 24
	wheelUp := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}
	updated, _ := m.Update(wheelUp)
	if got := updated.(model); got.cursor != 0 {
		t.Errorf("wheel up at the top: cursor = %d", got.cursor)
	}
	wheelDown := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}
	updated, _ = m.Update(wheelDown)
	if got := updated.(model); got.cursor != 1 {
		t.Errorf("wheel down: cursor = %d, want 1", got.cursor)
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

func TestViewFilterMode(t *testing.T) {
	m := testModel()
	m.mode = modeFilter
	m.filter = "node"
	m.width, m.height = 80, 24
	view := m.View()
	for _, want := range []string{"filter", "2 of 3", "enter apply", "esc clear"} {
		if !strings.Contains(view, want) {
			t.Errorf("filter UI missing %q:\n%s", want, view)
		}
	}
}
