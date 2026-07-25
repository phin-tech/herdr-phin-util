package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	labelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	errStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	focusStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	checkedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("108")).Bold(true)
	uncheckedDim = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	keyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))

	createButtonStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("236")).Background(lipgloss.Color("108")).Padding(0, 1)
	cancelButtonStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
)

// view renders the popup and records where everything landed, so the next
// mouse click can be tested against exactly what is on screen right now --
// nothing is clickable before it has been drawn at least once.
func (m *Model) view() string {
	var lines []string
	add := func(s string) { lines = append(lines, s) }

	add(titleStyle.Render("Smart workspace maker"))
	add("")

	add(fieldLabel("Link or name", m.focus == fieldLink))
	m.linkRow = len(lines)
	add(m.linkInput.View())
	add("")

	if m.linkInput.Value() != "" {
		add(dimStyle.Render(targetSummary(m.tgt)))
		add("")
	}

	m.toggleRow = len(lines)
	add(m.viewToggle())
	add("")

	add(fieldLabel("Prompt (typed, not submitted)", m.focus == fieldPrompt))
	m.promptTop = len(lines)
	promptLines := strings.Split(m.promptArea.View(), "\n")
	lines = append(lines, promptLines...)
	m.promptBot = len(lines) - 1
	add("")

	if m.err != nil {
		add(errStyle.Render(m.err.Error()))
		add("")
	} else if m.status != "" {
		add(dimStyle.Render(m.status))
		add("")
	}

	buttons := m.viewButtons()
	m.buttonsRow = len(lines)
	add(buttons)
	add("")
	add(m.viewHint())

	return strings.Join(lines, "\n")
}

func fieldLabel(text string, focused bool) string {
	if focused {
		return focusStyle.Render("▸ " + text)
	}
	return labelStyle.Render("  " + text)
}

func (m *Model) viewToggle() string {
	box := uncheckedDim.Render("[ ]")
	if m.agentOn {
		box = checkedStyle.Render("[x]")
	}
	kind := m.cfg.Agent.Kind
	text := box + " start " + kind + " and type the prompt below (unsent)"
	if m.focus == fieldToggle {
		return focusStyle.Render("▸ ") + text
	}
	return "  " + text
}

// viewButtons renders the Create/Cancel row and records the column ranges a
// click is tested against. Widths come from lipgloss.Width on the plain
// label, not the styled string, since ANSI escapes have zero visible width
// but are very much present in len().
func (m *Model) viewButtons() string {
	const createLabel = " Create (ctrl+s) "
	const cancelLabel = " Cancel (esc) "
	const gap = "   "

	m.createButtonX0 = 0
	m.createButtonX1 = lipgloss.Width(createLabel) - 1
	m.cancelButtonX0 = m.createButtonX1 + 1 + lipgloss.Width(gap)
	m.cancelButtonX1 = m.cancelButtonX0 + lipgloss.Width(cancelLabel) - 1

	if m.running {
		return dimStyle.Render("working...")
	}
	return createButtonStyle.Render(createLabel) + gap + cancelButtonStyle.Render(cancelLabel)
}

func (m *Model) viewHint() string {
	return keyStyle.Render("tab") + dimStyle.Render(" next field  ") +
		keyStyle.Render("space") + dimStyle.Render(" toggle agent  ") +
		keyStyle.Render("click or ctrl+s") + dimStyle.Render(" create  ") +
		keyStyle.Render("esc") + dimStyle.Render(" cancel")
}
