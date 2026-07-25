package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/open"
	"github.com/phin-tech/herdr-phin-util/internal/session"
)

// level is which list the picker is showing.
type level int

const (
	// levelProjects is the top: Spaces and checkouts.
	levelProjects level = iota
	// levelWorktrees is one repository's worktrees and branches.
	levelWorktrees
)

// Picker is the project picker popup: type to narrow, Enter to go.
//
// Unlike the workspace maker there is no prompt box and no form to fill in.
// Every row already knows what it means, so the whole interaction is meant to
// be a few characters and Enter -- anything else on screen would be in the
// way.
//
// It has two levels. The second is reached by descending into a repository,
// and the first is restored exactly as it was on the way back out: re-listing
// would be slower and would lose the filter someone had already typed.
type Picker struct {
	cfg     *config.Settings
	deps    session.Deps
	focuser session.Focuser

	level level
	// rootLevel is the level esc quits from. It is levelProjects for the
	// normal picker and levelWorktrees for the one opened straight into a
	// repository, which has nothing underneath it to go back to.
	rootLevel level
	repo      session.RepoContext
	// project* preserve the top level across a descent.
	projectAll    []session.Candidate
	projectFilter string
	projectCursor int

	all      []session.Candidate
	filtered []session.Candidate

	filter textinput.Model
	cursor int
	// offset is the first visible row, so a long list scrolls under a fixed
	// window rather than redrawing off the bottom of the popup.
	offset int

	agentOn bool

	width, height int

	running  bool
	status   string
	err      error
	result   open.Outcome
	picked   session.Candidate
	done     bool
	quitting bool

	// Hit regions recorded by the last render, same approach as the workspace
	// maker: nothing is clickable before it has been drawn.
	listTop     int
	visibleRows int
	toggleRow   int
}

// NewPicker builds the popup over an already-loaded candidate list. Listing is
// the caller's job so that a failure to reach Herdr is reported before a popup
// opens onto an empty box.
func NewPicker(cfg *config.Settings, deps session.Deps, focuser session.Focuser, candidates []session.Candidate) *Picker {
	filter := textinput.New()
	filter.Placeholder = filterPlaceholder(levelProjects)
	filter.Prompt = "> "
	filter.Focus()

	p := &Picker{
		cfg:        cfg,
		deps:       deps,
		focuser:    focuser,
		level:      levelProjects,
		all:        candidates,
		projectAll: candidates,
		filter:     filter,
		agentOn:    cfg.Agent.Enabled,
		width:      80,
		height:     24,
	}
	p.applyFilter()
	return p
}

// NewWorktreePicker opens straight at the worktree level for one repository,
// for the keybind that means "branches of the repo I am already in".
//
// esc quits rather than ascending: there is no project list underneath, and
// dropping someone into one they did not ask for would be a strange answer to
// a key that named a single repo.
func NewWorktreePicker(cfg *config.Settings, deps session.Deps, focuser session.Focuser, repo session.RepoContext, candidates []session.Candidate) *Picker {
	p := NewPicker(cfg, deps, focuser, candidates)
	p.level = levelWorktrees
	p.repo = repo
	p.rootLevel = levelWorktrees
	p.filter.Placeholder = filterPlaceholder(levelWorktrees)
	return p
}

// filterPlaceholder names what the current level is a list of.
func filterPlaceholder(l level) string {
	if l == levelWorktrees {
		return "filter branches and worktrees"
	}
	return "filter spaces and projects"
}

// canDescend reports whether a row has a repository behind it to look into.
func canDescend(c session.Candidate) bool {
	if c.Path == "" {
		return false
	}
	return c.Kind == session.KindProject || c.Kind == session.KindSpace
}

type descendMsg struct {
	repo       session.RepoContext
	candidates []session.Candidate
	err        error
}

// descend loads one repository's worktrees and branches. Both are I/O -- a
// socket round trip and two git invocations -- so it runs off the UI loop like
// any other blocking work here.
func (p *Picker) descend(c session.Candidate) tea.Cmd {
	if p.deps.Worktrees == nil || p.deps.Git == nil {
		return nil
	}

	p.running = true
	p.err = nil
	p.status = "reading " + c.Label + "..."

	lister, git, path := p.deps.Worktrees, p.deps.Git, c.Path
	return func() tea.Msg {
		candidates, repo, err := session.ListWorktrees(lister, git, path)
		return descendMsg{repo: repo, candidates: candidates, err: err}
	}
}

// ascend returns to the project level, restoring the filter and selection the
// descent interrupted.
func (p *Picker) ascend() {
	p.level = levelProjects
	p.repo = session.RepoContext{}
	p.all = p.projectAll
	p.filter.Placeholder = filterPlaceholder(levelProjects)
	p.err = nil
	p.status = ""
	p.filter.SetValue(p.projectFilter)
	p.applyFilter()
	p.cursor = p.projectCursor
	p.clampCursor()
}

type refreshMsg struct {
	candidates []session.Candidate
	err        error
}

// refresh fetches the repository's remote and rebuilds the list. This is the
// escape hatch for the cached-refs trade: the level opens instantly from what
// is already on disk, and this is how you say "go and look properly".
func (p *Picker) refresh() tea.Cmd {
	if p.level != levelWorktrees || p.deps.Worktrees == nil || p.deps.Git == nil {
		return nil
	}

	p.running = true
	p.err = nil
	p.status = "fetching " + p.repo.Name + "..."

	lister, git, root := p.deps.Worktrees, p.deps.Git, p.repo.Root
	return func() tea.Msg {
		if err := git.Refresh(root); err != nil {
			return refreshMsg{err: err}
		}
		candidates, _, err := session.ListWorktrees(lister, git, root)
		return refreshMsg{candidates: candidates, err: err}
	}
}

func (p *Picker) Init() tea.Cmd {
	return textinput.Blink
}

type pickResultMsg struct {
	out open.Outcome
	err error
}

func (p *Picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
		p.clampCursor()
		return p, nil

	case pickResultMsg:
		p.running = false
		if msg.err != nil {
			p.err = msg.err
			p.status = ""
			return p, nil
		}
		p.result = msg.out
		p.done = true
		p.quitting = true
		return p, tea.Quit

	case descendMsg:
		p.running = false
		p.status = ""
		if msg.err != nil {
			// Stay where we are: the project list is still perfectly usable,
			// and dropping into an empty worktree level would be worse than
			// saying why we did not.
			p.err = msg.err
			return p, nil
		}
		// Remember the top level only now that the descent has succeeded.
		p.projectFilter = p.filter.Value()
		p.projectCursor = p.cursor
		p.level = levelWorktrees
		p.repo = msg.repo
		p.all = msg.candidates
		p.filter.Placeholder = filterPlaceholder(levelWorktrees)
		p.filter.SetValue("")
		p.applyFilter()
		return p, nil

	case refreshMsg:
		p.running = false
		p.status = ""
		if msg.err != nil {
			p.err = msg.err
			return p, nil
		}
		p.all = msg.candidates
		p.applyFilter()
		return p, nil

	case tea.MouseMsg:
		return p.handleMouse(msg)

	case tea.KeyMsg:
		return p.handleKey(msg)
	}
	return p, nil
}

func (p *Picker) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if p.running {
		return p, nil
	}

	// The wheel moves the selection rather than scrolling under it. Enter acts
	// on the selection, so a window that had drifted away from the highlighted
	// row would be a picker that opens something you cannot see.
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		p.move(-1)
		return p, nil
	case tea.MouseButtonWheelDown:
		p.move(1)
		return p, nil
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return p, nil
	}

	if msg.Y == p.toggleRow {
		p.agentOn = !p.agentOn
		return p, nil
	}

	row := msg.Y - p.listTop
	if row >= 0 && row < p.visibleRows {
		index := p.offset + row
		if index < len(p.filtered) {
			// One click selects and goes. A picker whose rows needed a second
			// click to confirm would be slower than the keyboard it replaces.
			p.cursor = index
			return p, p.submit()
		}
	}
	return p, nil
}

func (p *Picker) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if p.running {
		if msg.String() == "ctrl+c" {
			p.quitting = true
			return p, tea.Quit
		}
		return p, nil
	}

	switch msg.String() {
	case "ctrl+c":
		p.quitting = true
		return p, tea.Quit

	case "esc":
		// Inside a repository, esc is "back" rather than "quit". Backing out
		// one level at a time is what makes descending safe to try -- unless
		// this level is where the picker started, in which case there is
		// nothing underneath and esc means what it usually does.
		if p.level == levelWorktrees && p.rootLevel != levelWorktrees {
			p.ascend()
			return p, nil
		}
		p.quitting = true
		return p, tea.Quit

	case "enter":
		return p, p.submit()

	case "right", "ctrl+w":
		if p.level == levelProjects {
			if c, ok := p.selected(); ok && canDescend(c) {
				return p, p.descend(c)
			}
		}
		return p, nil

	case "left":
		if p.level == levelWorktrees && p.rootLevel != levelWorktrees {
			p.ascend()
		}
		return p, nil

	case "ctrl+r":
		return p, p.refresh()

	case "up", "ctrl+p":
		p.move(-1)
		return p, nil
	case "down", "ctrl+n":
		p.move(1)
		return p, nil
	case "pgup":
		p.move(-p.pageSize())
		return p, nil
	case "pgdown":
		p.move(p.pageSize())
		return p, nil
	case "home":
		p.move(-len(p.filtered))
		return p, nil
	case "end":
		p.move(len(p.filtered))
		return p, nil

	// The filter box owns every printable key, so the agent toggle needs one
	// that is not a character.
	case "ctrl+a":
		p.agentOn = !p.agentOn
		return p, nil
	}

	before := p.filter.Value()
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(msg)
	if p.filter.Value() != before {
		p.applyFilter()
	}
	return p, cmd
}

// applyFilter narrows the list without reordering it. fzf would rank by score,
// but the order here is meaningful -- open Spaces before checkouts -- and
// having rows jump between positions as you type costs more than ranking
// gains.
func (p *Picker) applyFilter() {
	query := strings.TrimSpace(p.filter.Value())
	if query == "" {
		p.filtered = p.all
	} else {
		out := make([]session.Candidate, 0, len(p.all)+1)
		for _, c := range p.all {
			if matches(query, c) {
				out = append(out, c)
			}
		}
		// Inside a repository, text that names no existing branch becomes an
		// offer to create it. That is how a new branch gets made without a
		// separate mode to enter -- you are already typing its name.
		if p.level == levelWorktrees && !hasExactBranch(p.all, query) {
			out = append(out, session.NewBranchCandidate(p.repo, query))
		}
		p.filtered = out
	}
	p.cursor = 0
	p.offset = 0
}

// hasExactBranch reports whether the query already names a row exactly, in
// which case offering to create it would be offering a duplicate.
func hasExactBranch(candidates []session.Candidate, query string) bool {
	for _, c := range candidates {
		if c.Label == query || c.Branch == query {
			return true
		}
	}
	return false
}

// matches is a subsequence test against the label and its detail line, so
// "phnutl" finds herdr-phin-util and "srcphin" finds it by path.
func matches(query string, c session.Candidate) bool {
	return subsequence(strings.ToLower(query), strings.ToLower(c.Label+" "+c.Detail))
}

// subsequence indexes the query by rune rather than byte: a path with an
// accent in it should not make the match walk off the middle of a character.
func subsequence(query, text string) bool {
	q := []rune(query)
	if len(q) == 0 {
		return true
	}
	qi := 0
	for _, r := range text {
		if q[qi] == r {
			qi++
			if qi == len(q) {
				return true
			}
		}
	}
	return false
}

func (p *Picker) move(delta int) {
	if len(p.filtered) == 0 {
		return
	}
	p.cursor += delta
	p.clampCursor()
}

// clampCursor keeps the selection inside the list and the scroll window around
// the selection, which is the only place either invariant is enforced.
func (p *Picker) clampCursor() {
	if len(p.filtered) == 0 {
		p.cursor, p.offset = 0, 0
		return
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor > len(p.filtered)-1 {
		p.cursor = len(p.filtered) - 1
	}

	size := p.pageSize()
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+size {
		p.offset = p.cursor - size + 1
	}
	if max := len(p.filtered) - size; p.offset > max {
		p.offset = max
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

// chromeHeight is every line the popup draws that is not a list row: title,
// filter, toggle, hint, and the blank lines between them.
const chromeHeight = 9

func (p *Picker) pageSize() int {
	size := p.height - chromeHeight
	if size < 1 {
		return 1
	}
	return size
}

// selected reports the highlighted row, if there is one.
func (p *Picker) selected() (session.Candidate, bool) {
	if p.cursor < 0 || p.cursor >= len(p.filtered) {
		return session.Candidate{}, false
	}
	return p.filtered[p.cursor], true
}

// submit runs the pick on a worker goroutine. Focusing is quick, but creating
// a Space and waiting for an agent is not, so neither blocks the UI.
func (p *Picker) submit() tea.Cmd {
	candidate, ok := p.selected()
	if !ok {
		return nil
	}

	p.running = true
	p.err = nil
	p.picked = candidate
	p.status = statusFor(candidate)

	agent := p.agentOn
	opts := open.Options{Agent: &agent}
	deps := p.deps
	focuser := p.focuser
	cfg := p.cfg
	repo := p.repo

	if p.level == levelWorktrees && candidate.Kind != session.KindSpace {
		// A Space is a Space at either level, so focusing stays with the
		// project-level dispatch; everything else here is worktree-shaped.
		return func() tea.Msg {
			out, err := session.OpenWorktree(deps, cfg, repo, candidate, opts)
			return pickResultMsg{out: out, err: err}
		}
	}

	return func() tea.Msg {
		out, err := session.Open(deps, focuser, cfg, candidate, opts)
		return pickResultMsg{out: out, err: err}
	}
}

func statusFor(c session.Candidate) string {
	switch c.Kind {
	case session.KindSpace:
		return "switching to " + c.Label + "..."
	case session.KindNewBranch:
		return "creating branch " + c.Label + "..."
	case session.KindRemoteBranch:
		return "fetching " + c.Label + "..."
	default:
		return "opening " + c.Label + "..."
	}
}

// Result reports what the picker decided, for the caller to notify or log once
// the Program has exited normally rather than been cancelled.
func (p *Picker) Result() (open.Outcome, session.Candidate, error, bool) {
	return p.result, p.picked, p.err, p.done
}

func (p *Picker) View() string {
	if p.quitting {
		return ""
	}
	return p.pickerView()
}
