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
	if m.running {
		return m.viewRunning()
	}

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
		// The checklist survives a failure: which step it got to is most of
		// what you want to know before deciding whether to press Create again.
		if steps := m.progress.render(m.width); len(steps) > 0 {
			lines = append(lines, steps...)
			add("")
		}
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

// viewRunning replaces the form with the checklist for as long as the run
// lasts. The form is inert while running -- every key and click is ignored --
// so leaving it on screen would be showing a control panel nobody can touch,
// in place of the one thing there is to say.
//
// Hit regions are pushed off screen rather than left at their old rows: a
// click cannot land while running, but a stale region outliving the layout
// that produced it is the kind of thing that stops being true later.
func (m *Model) viewRunning() string {
	m.linkRow, m.toggleRow = -1, -1
	m.promptTop, m.promptBot = -1, -1
	m.buttonsRow = -1

	var lines []string
	add := func(s string) { lines = append(lines, s) }

	add(titleStyle.Render("Smart workspace maker"))
	add("")
	if v := m.linkInput.Value(); v != "" {
		add(dimStyle.Render(targetSummary(m.tgt)))
		add("")
	}

	steps := m.progress.render(m.width)
	if len(steps) == 0 {
		// The gap between pressing Create and the first step reporting. Saying
		// "starting..." beats an empty box that looks like nothing happened.
		add(dimStyle.Render("starting..."))
	}
	lines = append(lines, steps...)
	add("")
	add(dimStyle.Render("total ") + progressTimeStyle.Render(shortDuration(m.progress.total())))
	add("")
	add(keyStyle.Render("ctrl+c") + dimStyle.Render(" cancel"))

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
