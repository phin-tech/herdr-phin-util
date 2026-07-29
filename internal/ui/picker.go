package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/open"
	"github.com/phin-tech/herdr-phin-util/internal/session"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// level is which list the picker is showing.
type level int

const (
	// levelProjects is the top: Spaces and checkouts.
	levelProjects level = iota
	// levelWorktrees is one repository's worktrees and branches.
	levelWorktrees
	// levelSetups is the recipes that apply to one highlighted row. Unlike the
	// other two it is not a level of the same tree -- it is a question about
	// the row you were already on, which is why it is reached with its own key
	// rather than by descending.
	levelSetups
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

	// saved is the level ctrl+t interrupted, restored on the way back. The
	// setup level can be entered from either of the other two, so unlike the
	// descent it cannot assume where it came from.
	saved *savedLevel
	// pending is the row a picked setup will be applied to. The setup rows
	// themselves are not things to open.
	pending session.Candidate

	// workspaces is kept so a pasted link can be matched against the open
	// Spaces by label without another round trip.
	workspaces []herdr.Workspace

	all      []session.Candidate
	filtered []session.Candidate
	// linkMode records that the current rows came from resolving a pasted
	// reference rather than filtering the list, which the count summary has to
	// know: "1 of 21" would imply the other 20 were considered and rejected.
	linkMode bool

	filter textinput.Model
	cursor int
	// offset is the first visible row, so a long list scrolls under a fixed
	// window rather than redrawing off the bottom of the popup.
	offset int

	agentOn bool

	// promptArea is the maker's "read it before it is sent" affordance,
	// reachable with ctrl+e. It is hidden by default: most picks switch to a
	// Space and never start an agent, so a textarea on screen would be dead
	// weight for the common case.
	promptArea   textarea.Model
	editing      bool
	promptEdited bool

	width, height int

	running  bool
	status   string
	err      error
	result   open.Outcome
	picked   session.Candidate
	done     bool
	quitting bool

	// progress is the checklist for a pick that is being opened, nil until one
	// starts. Descending and refreshing keep the plain status line: they are
	// one step each, and a one-item checklist is worse than a sentence.
	progress *progressList
	events   progressChannel

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

	prompt := textarea.New()
	prompt.Placeholder = "prompt typed into the agent (not submitted for you)"
	prompt.ShowLineNumbers = false
	prompt.SetHeight(6)

	p := &Picker{
		promptArea: prompt,
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

// WithWorkspaces supplies the open Spaces a pasted link is matched against.
// Without it the picker still works; a link simply always reads as "create",
// which is what it did before this existed.
func (p *Picker) WithWorkspaces(workspaces []herdr.Workspace) *Picker {
	p.workspaces = workspaces
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
	switch l {
	case levelWorktrees:
		return "filter branches, or type a new branch name"
	case levelSetups:
		return "filter setups"
	default:
		return "filter, or paste a PR / issue / Linear link"
	}
}

// savedLevel is everything the setup level has to put back when it closes.
type savedLevel struct {
	level  level
	all    []session.Candidate
	filter string
	cursor int
	offset int
	link   bool
}

// enterSetups opens the setup list for the highlighted row.
//
// The rows are read from disk on the spot rather than off the UI loop: it is a
// handful of small files in a directory, and a level that appeared a beat
// after the key was pressed would feel worse than one that costs a stat.
func (p *Picker) enterSetups() {
	c, ok := p.selected()
	if !ok || !offersSetups(c) {
		return
	}

	rows := session.SetupRows(p.deps.Setups, p.cfg, c)

	p.saved = &savedLevel{
		level:  p.level,
		all:    p.all,
		filter: p.filter.Value(),
		cursor: p.cursor,
		offset: p.offset,
		link:   p.linkMode,
	}
	p.pending = c
	p.level = levelSetups
	p.all = rows
	p.err = nil
	p.status = ""
	p.filter.Placeholder = filterPlaceholder(levelSetups)
	p.filter.SetValue("")
	p.applyFilter()
}

// leaveSetups puts back the level ctrl+t was pressed on, filter and selection
// intact -- the same contract ascend has, for the same reason: a level you can
// back out of losing nothing is one you will try.
func (p *Picker) leaveSetups() {
	if p.saved == nil {
		return
	}
	s := p.saved
	p.saved = nil
	p.pending = session.Candidate{}
	p.level = s.level
	p.all = s.all
	p.filter.Placeholder = filterPlaceholder(s.level)
	p.err = nil
	p.status = ""
	p.filter.SetValue(s.filter)
	p.applyFilter()
	p.linkMode = s.link
	p.cursor = s.cursor
	p.offset = s.offset
	p.clampCursor()
}

// offersSetups reports whether a row is one a setup could be applied to.
// Switching to a Space builds nothing, so there is no layout to choose.
func offersSetups(c session.Candidate) bool {
	return startsAnAgent(c)
}

// canDescend reports whether a row has a repository behind it to look into.
func canDescend(c session.Candidate) bool {
	switch c.Kind {
	case session.KindClone:
		// Nothing on disk yet, but the reference is enough to go and get it.
		return c.Target.Owner != "" && c.Target.Repo != ""
	case session.KindProject, session.KindSpace:
		return c.Path != ""
	default:
		return false
	}
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

	// A repository that is not here yet has to be fetched before it has any
	// branches to show. It is the same descent, with one slow step in front.
	if c.Kind == session.KindClone {
		p.status = "cloning " + c.Label + "..."
		deps, cfg, tgt := p.deps, p.cfg, c.Target
		return func() tea.Msg {
			candidates, repo, err := session.CloneAndList(deps, cfg, tgt)
			return descendMsg{repo: repo, candidates: candidates, err: err}
		}
	}

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

	case progressMsg:
		p.progress.apply(open.Event(msg))
		return p, waitForProgress(p.events)

	case progressTickMsg:
		if !p.running {
			return p, nil
		}
		return p, tickProgress()

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

	// While the prompt box is open it owns the keyboard, apart from the two
	// keys that close it again.
	if p.editing {
		switch msg.String() {
		case "ctrl+c":
			p.quitting = true
			return p, tea.Quit
		case "esc", "ctrl+e":
			p.editing = false
			p.promptArea.Blur()
			return p, nil
		case "ctrl+s", "enter":
			if msg.String() == "ctrl+s" {
				return p, p.submit()
			}
		}
		before := p.promptArea.Value()
		var cmd tea.Cmd
		p.promptArea, cmd = p.promptArea.Update(msg)
		if p.promptArea.Value() != before {
			p.promptEdited = true
		}
		return p, cmd
	}

	switch msg.String() {
	case "ctrl+c":
		p.quitting = true
		return p, tea.Quit

	case "ctrl+e":
		return p, p.toggleEditor()

	case "ctrl+t":
		if p.level != levelSetups {
			p.enterSetups()
		}
		return p, nil

	case "esc":
		// Inside a repository, esc is "back" rather than "quit". Backing out
		// one level at a time is what makes descending safe to try -- unless
		// this level is where the picker started, in which case there is
		// nothing underneath and esc means what it usually does.
		if p.level == levelSetups {
			p.leaveSetups()
			return p, nil
		}
		if p.level == levelWorktrees && p.rootLevel != levelWorktrees {
			p.ascend()
			return p, nil
		}
		p.quitting = true
		return p, tea.Quit

	case "enter":
		return p, p.submit()

	// tab descends, shift+tab comes back. Deliberately not the arrow keys:
	// the input box holds pasted URLs now, and text you cannot move a cursor
	// through is text you cannot correct.
	//
	// "Deeper" is one meaning, applied to whatever the row actually has
	// underneath it: a repository has worktrees, and a worktree, a branch or a
	// pasted link has only the question of how to build it. So tab lands on
	// the setups when there is no level below -- which is also what makes the
	// flagship path one pass: paste a PR, tab, pick the review layout, enter.
	case "tab", "ctrl+w":
		if p.level == levelSetups {
			return p, nil
		}
		// Only the project level has a repository underneath it. A Space row
		// one level down is a worktree that is already open, and re-listing
		// the repository it belongs to would be going sideways, not deeper.
		if c, ok := p.selected(); ok && p.level == levelProjects && canDescend(c) {
			return p, p.descend(c)
		}
		p.enterSetups()
		return p, nil

	case "shift+tab":
		if p.level == levelSetups {
			p.leaveSetups()
			return p, nil
		}
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

// applyFilter decides what the rows are, from what has been typed.
//
// This is the whole idea behind one input: the query's *shape* selects the
// result set, rather than a mode being chosen up front. A reference resolves
// to the single thing it names; anything else filters the list.
func (p *Picker) applyFilter() {
	query := strings.TrimSpace(p.filter.Value())

	// A pasted reference is not a filter that happens to match nothing -- it
	// is a query with exactly one answer, so it replaces the list outright.
	if c, ok := session.ResolveLink(p.workspaces, p.cfg, p.parseQuery(query)); ok {
		p.filtered = []session.Candidate{c}
		p.linkMode = true
		p.cursor, p.offset = 0, 0
		return
	}
	p.linkMode = false

	if query == "" {
		p.filtered = p.all
		p.cursor, p.offset = 0, 0
		return
	}

	out := rank(query, p.all)
	// Inside a repository, text that names no existing branch becomes an
	// offer to create it. That is how a new branch gets made without a
	// separate mode to enter -- you are already typing its name.
	if p.level == levelWorktrees && !hasExactBranch(p.all, query) {
		out = append(out, session.NewBranchCandidate(p.repo, query))
	}
	p.filtered = out
	p.cursor = 0
	p.offset = 0
}

// parseQuery classifies what has been typed.
//
// "owner/repo" shorthand is only recognised at the project level. One level
// down that shape is overwhelmingly a branch name -- "codex/iterm-split" --
// and nothing but context tells the two apart.
func (p *Picker) parseQuery(query string) target.Target {
	if tgt := target.Parse(query); tgt.Kind != target.KindPlain {
		return tgt
	}
	if p.level == levelProjects {
		if tgt, ok := target.ParseRepoShorthand(query); ok {
			return tgt
		}
	}
	return target.Parse(query)
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

// Match tiers, best first. The gap between tierLabelSubsequence and tierDetail
// is the important one: a query that names something by label should never be
// buried under rows that only matched because every path contains
// "~/src/github.com".
const (
	tierExact = iota
	tierPrefix
	tierSubstring
	tierLabelSubsequence
	tierDetail
	tierNone
)

// rank filters and orders the candidates for a non-empty query.
//
// The empty query is left alone by the caller, because that order is meaningful
// -- open Spaces before checkouts. Once something has been typed, though, the
// list is being rebuilt from scratch on every keystroke anyway, and burying the
// thing you named under thirteen incidental path matches costs more than the
// stability is worth.
//
// Detail matches are a fallback rather than a tier: they only survive if
// nothing matched by label, so "srcphin" still finds a repo by path without
// every loose path match riding along on a query that already named a label.
func rank(query string, all []session.Candidate) []session.Candidate {
	q := strings.ToLower(strings.TrimSpace(query))

	type scored struct {
		c    session.Candidate
		tier int
	}
	var hits []scored
	best := tierNone
	for _, c := range all {
		t := tierOf(q, c)
		if t == tierNone {
			continue
		}
		if t < best {
			best = t
		}
		hits = append(hits, scored{c, t})
	}

	out := make([]session.Candidate, 0, len(hits)+1)
	// Two passes rather than a sort: there are only a handful of tiers, and
	// walking them in order keeps each tier in its original -- meaningful --
	// order for free.
	for tier := tierExact; tier <= tierDetail; tier++ {
		if tier == tierDetail && best < tierDetail {
			break
		}
		for _, h := range hits {
			if h.tier == tier {
				out = append(out, h.c)
			}
		}
	}
	return out
}

// tierOf scores one candidate against an already-lowercased query.
func tierOf(query string, c session.Candidate) int {
	label := strings.ToLower(c.Label)
	switch {
	case label == query:
		return tierExact
	case strings.HasPrefix(label, query):
		return tierPrefix
	case strings.Contains(label, query):
		return tierSubstring
	case subsequence(query, label):
		return tierLabelSubsequence
	case subsequence(query, strings.ToLower(c.Detail)):
		return tierDetail
	}
	return tierNone
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

	agent := p.agentOn
	opts := open.Options{Agent: &agent}

	// A setup row is not a thing to open. It answers "how", and the row it was
	// offered for -- held in pending since ctrl+t -- is still the "what".
	level := p.level
	if candidate.Kind == session.KindSetup {
		opts.Setup = candidate.Setup
		candidate = p.pending
		if p.saved != nil {
			level = p.saved.level
		}
	}

	p.running = true
	p.err = nil
	p.picked = candidate
	p.status = statusFor(candidate)

	if p.promptEdited {
		// Edited text wins outright over the template, the same rule the
		// workspace maker follows.
		opts.Prompt = p.promptArea.Value()
	}
	focuser := p.focuser
	cfg := p.cfg
	repo := p.repo

	// Switching to a Space that already exists is instant -- there is nothing
	// to clone, cut or start -- so it keeps the status line rather than
	// flashing a checklist that would be finished before it was read.
	var run tea.Cmd
	deps := p.deps
	if candidate.Kind != session.KindSpace {
		p.progress = newProgressList()
		p.events = newProgressChannel()
		deps.Open.Progress = p.events.reporter()
	}

	if level == levelWorktrees && candidate.Kind != session.KindSpace {
		// A Space is a Space at either level, so focusing stays with the
		// project-level dispatch; everything else here is worktree-shaped.
		run = func() tea.Msg {
			out, err := session.OpenWorktree(deps, cfg, repo, candidate, opts)
			return pickResultMsg{out: out, err: err}
		}
	} else {
		run = func() tea.Msg {
			out, err := session.Open(deps, focuser, cfg, candidate, opts)
			return pickResultMsg{out: out, err: err}
		}
	}

	if p.progress == nil {
		return run
	}
	return tea.Batch(run, waitForProgress(p.events), tickProgress())
}

// toggleEditor opens the prompt box for the highlighted row, pre-filled with
// what the template would produce.
//
// It is only offered where it means something: switching to a Space starts no
// agent, so there would be no prompt to edit.
func (p *Picker) toggleEditor() tea.Cmd {
	c, ok := p.selected()
	if !ok || !startsAnAgent(c) {
		return nil
	}

	if !p.promptEdited {
		if preview, err := open.PreviewPrompt(p.cfg, promptTargetFor(c)); err == nil {
			p.promptArea.SetValue(preview)
		}
	}
	p.editing = true
	p.promptArea.Focus()
	return textarea.Blink
}

// startsAnAgent reports whether picking a row would run the agent step, which
// is the only case where a prompt exists to edit.
func startsAnAgent(c session.Candidate) bool {
	switch c.Kind {
	case session.KindSpace, session.KindPrunable:
		return false
	default:
		return true
	}
}

// promptTargetFor is what the prompt template renders against. A link row
// carries its own parsed target; everything else is a checkout, which uses the
// project template.
func promptTargetFor(c session.Candidate) target.Target {
	if c.Kind == session.KindLink {
		return c.Target
	}
	return target.Target{Kind: target.KindProject, Text: c.Label}
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
