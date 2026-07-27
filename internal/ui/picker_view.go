package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/phin-tech/herdr-phin-util/internal/session"
)

var (
	selectedRowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	spaceTagStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	projectTagStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	branchTagStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("179"))
	remoteTagStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	createTagStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
)

// pickerView renders the popup and records where the rows landed, so the next
// click can be tested against exactly what is on screen right now.
func (p *Picker) pickerView() string {
	if p.running && p.progress != nil {
		return p.viewRunning()
	}

	var lines []string
	add := func(s string) { lines = append(lines, s) }

	add(p.viewTitle())
	add("")

	add(p.filter.View())
	add(dimStyle.Render(p.countSummary()))
	add("")

	if p.editing {
		// The prompt box takes the list's space rather than sitting under it:
		// while you are writing a prompt, the list is not what you are looking
		// at, and a popup has no room to pretend otherwise.
		p.visibleRows = 0
		add(labelStyle.Render("  Prompt (typed, not submitted)"))
		lines = append(lines, strings.Split(p.promptArea.View(), "\n")...)
		add("")
	} else {
		p.listTop = len(lines)
		rows := p.viewRows()
		p.visibleRows = len(rows)
		lines = append(lines, rows...)
		add("")
	}

	p.toggleRow = len(lines)
	add(p.viewAgentToggle())
	add("")

	switch {
	case p.err != nil:
		// A failed pick keeps its checklist: which step it reached is most of
		// the diagnosis, and the list is gone by the time the error is read
		// otherwise.
		if steps := p.progress.render(p.width); len(steps) > 0 {
			lines = append(lines, steps...)
			add("")
		}
		add(errStyle.Render(p.err.Error()))
	case p.status != "":
		add(dimStyle.Render(p.status))
	default:
		add(p.viewPickerHint())
	}

	return strings.Join(lines, "\n")
}

// viewRunning replaces the list with the checklist while a pick is being
// opened, for the same reason the workspace maker does it: the list is inert
// during the run, and what the run is doing is the only thing there is to say.
func (p *Picker) viewRunning() string {
	p.listTop, p.visibleRows, p.toggleRow = 0, 0, -1

	var lines []string
	add := func(s string) { lines = append(lines, s) }

	add(p.viewTitle())
	add("")
	if p.picked.Label != "" {
		add(dimStyle.Render(statusFor(p.picked)))
		add("")
	}

	steps := p.progress.render(p.width)
	if len(steps) == 0 {
		add(dimStyle.Render("starting..."))
	}
	lines = append(lines, steps...)
	add("")
	add(dimStyle.Render("total ") + progressTimeStyle.Render(shortDuration(p.progress.total())))
	add("")
	add(keyStyle.Render("ctrl+c") + dimStyle.Render(" cancel"))

	return strings.Join(lines, "\n")
}

// viewTitle is a breadcrumb, so it is always clear which level esc goes back
// to and which repository the rows belong to.
func (p *Picker) viewTitle() string {
	if p.level == levelSetups {
		// The breadcrumb names the row the setup will be applied to, since
		// that is the thing the choice is about.
		return titleStyle.Render("Open a project") +
			dimStyle.Render(" › ") +
			titleStyle.Render(p.pending.Label) +
			dimStyle.Render(" › ") +
			titleStyle.Render("setup")
	}
	if p.level == levelWorktrees {
		return titleStyle.Render("Open a project") +
			dimStyle.Render(" › ") +
			titleStyle.Render(p.repo.Name)
	}
	return titleStyle.Render("Open a project")
}

func (p *Picker) countSummary() string {
	if p.linkMode {
		if c, ok := p.selected(); ok && c.Kind == session.KindSpace {
			return "already open"
		}
		return "one result"
	}
	if p.level == levelWorktrees && len(p.all) == 0 {
		return "no worktrees or branches found"
	}
	if p.level == levelSetups && len(p.all) == 1 {
		// Only the default row: nothing applies here, which is a different
		// thing from nothing being defined at all.
		return "no setups apply to this row"
	}
	if len(p.all) == 0 {
		return "nothing found — check [projects].roots in your config"
	}
	if len(p.filtered) == 0 {
		return "no match"
	}
	if len(p.filtered) == len(p.all) {
		return fmt.Sprintf("%d total", len(p.all))
	}
	return fmt.Sprintf("%d of %d", len(p.filtered), len(p.all))
}

// labelColumn bounds how wide the name column is allowed to grow. One
// unusually long repo name should not push every path off the right edge.
const (
	labelColumnMin = 12
	labelColumnMax = 32
)

// currentSuffix marks the Space you are already in. It is still selectable, but
// saying so stops it reading as a duplicate of a project row.
const currentSuffix = " (current)"

// viewRows draws the visible slice of the list, padded to a fixed height so
// the toggle and hint below it do not jump around as the filter narrows.
//
// The name column is sized to the rows actually on screen, so the paths line
// up into a column that can be read down rather than hunted along.
func (p *Picker) viewRows() []string {
	size := p.pageSize()
	visible := make([]session.Candidate, 0, size)
	for i := 0; i < size; i++ {
		index := p.offset + i
		if index >= len(p.filtered) {
			break
		}
		visible = append(visible, p.filtered[index])
	}

	labelWidth := labelColumnMin
	for _, c := range visible {
		if w := plainLabelWidth(c); w > labelWidth {
			labelWidth = w
		}
	}
	if labelWidth > labelColumnMax {
		labelWidth = labelColumnMax
	}

	out := make([]string, 0, size)
	for i := 0; i < size; i++ {
		if i >= len(visible) {
			out = append(out, "")
			continue
		}
		out = append(out, p.viewRow(visible[i], p.offset+i == p.cursor, labelWidth))
	}
	return out
}

// plainLabelWidth measures what a row's name column will occupy once drawn,
// before any styling is applied -- ANSI escapes have no visible width but do
// have length.
func plainLabelWidth(c session.Candidate) int {
	width := len([]rune(c.Label))
	if c.Focused {
		width += len(currentSuffix)
	}
	return width
}

// rowTag is the fixed-width kind marker at the head of every row. One word per
// kind, padded to a column, so the list can be scanned down for "what would
// this actually do".
func rowTag(k session.Kind) string {
	switch k {
	case session.KindSpace:
		return spaceTagStyle.Render("open  ")
	case session.KindWorktree:
		return projectTagStyle.Render("tree  ")
	case session.KindBranch:
		return branchTagStyle.Render("local ")
	case session.KindRemoteBranch:
		return remoteTagStyle.Render("remote")
	case session.KindNewBranch:
		return createTagStyle.Render("create")
	case session.KindLink:
		return createTagStyle.Render("link  ")
	case session.KindClone:
		return remoteTagStyle.Render("clone ")
	case session.KindPrunable:
		return errStyle.Render("gone  ")
	case session.KindSetup:
		return createTagStyle.Render("setup ")
	default: // KindProject
		return projectTagStyle.Render("new   ")
	}
}

func (p *Picker) viewRow(c session.Candidate, selected bool, labelWidth int) string {
	tag := rowTag(c.Kind)

	marker := "  "
	name := c.Label
	if selected {
		marker = focusStyle.Render("▸ ")
	}

	// Truncation happens on the plain text, so a name clipped to the column
	// never leaves a half-written escape sequence behind.
	plain := name
	if c.Focused {
		plain += currentSuffix
	}
	if len([]rune(plain)) > labelWidth {
		name = truncateTail(name, labelWidth-boolInt(c.Focused)*len(currentSuffix))
		plain = name
		if c.Focused {
			plain += currentSuffix
		}
	}

	styled := name
	if selected {
		styled = selectedRowStyle.Render(name)
	}
	if c.Focused {
		styled += dimStyle.Render(currentSuffix)
	}
	padding := labelWidth - len([]rune(plain))
	if padding < 0 {
		padding = 0
	}

	row := marker + tag + "  " + styled + strings.Repeat(" ", padding)
	if c.Detail != "" {
		row += "  " + dimStyle.Render(truncate(c.Detail, p.detailWidth(row)))
	}
	return row
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// truncateTail clips a name from the right, which is where repo names differ
// least -- the opposite of a path, whose tail is the identifying part.
func truncateTail(s string, width int) string {
	runes := []rune(s)
	if width < 1 {
		width = 1
	}
	if len(runes) <= width {
		return s
	}
	return string(runes[:width-1]) + "…"
}

// detailWidth is what is left of the line once the row's own text is drawn,
// measured with lipgloss so the ANSI escapes in the styled prefix do not count
// against it.
func (p *Picker) detailWidth(prefix string) int {
	width := p.width - lipgloss.Width(prefix) - 2
	if width < 8 {
		return 8
	}
	return width
}

// truncate keeps the tail of a path rather than the head: ".../owner/repo"
// identifies a checkout, "/Users/someone/src/git..." does not.
func truncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 3 {
		return string(runes[len(runes)-width:])
	}
	return "…" + string(runes[len(runes)-(width-1):])
}

func (p *Picker) viewAgentToggle() string {
	box := uncheckedDim.Render("[ ]")
	if p.agentOn {
		box = checkedStyle.Render("[x]")
	}
	text := box + " start " + p.cfg.Agent.Kind + " in new spaces"
	if p.level == levelSetups {
		// A setup names its own agents per pane, so the one configured kind
		// is not what will be started.
		return "  " + box + dimStyle.Render(" the setup decides what runs")
	}
	if c, ok := p.selected(); ok && c.Kind == session.KindSpace {
		// Switching to something already running never starts an agent, so
		// the toggle would otherwise look like it was being ignored.
		text += dimStyle.Render("  (not used when switching)")
	}
	return "  " + text
}

// tabHint names what tab would do on the highlighted row, and reports false
// when it would do nothing at all -- an open Space builds nothing, so it has
// neither worktrees worth descending into nor a layout to choose.
func (p *Picker) tabHint() (string, bool) {
	c, ok := p.selected()
	if !ok {
		return "", false
	}
	if p.level == levelProjects && canDescend(c) {
		if c.Kind == session.KindClone {
			return " clone & branch  ", true
		}
		return " worktrees  ", true
	}
	if offersSetups(c) {
		return " setup  ", true
	}
	return "", false
}

func (p *Picker) viewPickerHint() string {
	if p.editing {
		return keyStyle.Render("ctrl+s") + dimStyle.Render(" open  ") +
			keyStyle.Render("ctrl+e") + dimStyle.Render(" back to the list  ") +
			keyStyle.Render("esc") + dimStyle.Render(" back")
	}

	hint := keyStyle.Render("↑↓") + dimStyle.Render(" move  ") +
		keyStyle.Render("enter") + dimStyle.Render(" open  ")

	if p.level == levelSetups {
		// Nothing else applies here: the row is already chosen, and this level
		// only decides how it gets built.
		return hint +
			keyStyle.Render("shift+tab") + dimStyle.Render(" back  ") +
			keyStyle.Render("esc") + dimStyle.Render(" back")
	}

	// Only advertise the prompt editor where it would do something.
	if c, ok := p.selected(); ok && startsAnAgent(c) && p.agentOn {
		hint += keyStyle.Render("ctrl+e") + dimStyle.Render(" prompt  ")
	}

	// tab is named for whatever the highlighted row actually has underneath
	// it, since that is the only way one key with one meaning reads as one
	// key: worktrees under a repository, setups under everything else.
	if label, ok := p.tabHint(); ok {
		hint += keyStyle.Render("tab") + dimStyle.Render(label)
	}

	if p.level == levelWorktrees {
		// esc only goes back when there is a level underneath to go back to.
		escLabel := " back"
		back := ""
		if p.rootLevel == levelWorktrees {
			escLabel = " cancel"
		} else {
			back = keyStyle.Render("shift+tab") + dimStyle.Render(" back  ")
		}
		return hint + back +
			keyStyle.Render("ctrl+r") + dimStyle.Render(" fetch  ") +
			keyStyle.Render("ctrl+a") + dimStyle.Render(" agent  ") +
			keyStyle.Render("esc") + dimStyle.Render(escLabel)
	}
	return hint +
		keyStyle.Render("ctrl+a") + dimStyle.Render(" agent  ") +
		keyStyle.Render("esc") + dimStyle.Render(" cancel")
}
