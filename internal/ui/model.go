// Package ui implements the "smart workspace maker" popup: paste a link,
// review what it resolved to, and either send it as-is or type over the
// prompt before it goes to the agent.
package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/open"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// field is which control has keyboard focus.
type field int

const (
	fieldLink field = iota
	fieldToggle
	fieldPrompt
)

// nextFocus cycles link -> toggle -> prompt -> link, in either direction.
func nextFocus(f field, forward bool) field {
	if forward {
		return (f + 1) % 3
	}
	return (f + 2) % 3
}

// Model is the popup.
type Model struct {
	cfg  *config.Settings
	deps open.Deps

	linkInput  textinput.Model
	promptArea textarea.Model
	agentOn    bool
	// promptEdited tracks whether the user has typed into the prompt box
	// directly. Once they have, their text wins outright over the template --
	// re-deriving it from the link on every keystroke would make an edit feel
	// like it kept getting silently discarded.
	promptEdited bool

	focus field
	tgt   target.Target

	width, height int
	status        string
	err           error

	running  bool
	result   open.Outcome
	done     bool
	quitting bool

	// Hit regions, recorded during the last render and consulted on the next
	// click -- rendered once, clicked against many times, same as Phin Board's
	// link tracking.
	linkRow                        int
	toggleRow                      int
	promptTop, promptBot           int
	buttonsRow                     int
	createButtonX0, createButtonX1 int
	cancelButtonX0, cancelButtonX1 int
}

// New builds the initial model. agentOn defaults to the config's toggle, and
// deps.Cwd is where a Linear or plain target's Space lands.
func New(cfg *config.Settings, deps open.Deps) *Model {
	link := textinput.New()
	link.Placeholder = "paste a GitHub PR / Linear issue link, or type a name"
	link.Prompt = "> "
	link.Focus()

	prompt := textarea.New()
	prompt.Placeholder = "prompt typed into the agent (not submitted for you)"
	prompt.ShowLineNumbers = false
	prompt.SetHeight(6)

	m := &Model{
		cfg:        cfg,
		deps:       deps,
		linkInput:  link,
		promptArea: prompt,
		agentOn:    cfg.Agent.Enabled,
		focus:      fieldLink,
		width:      80,
		height:     24,
	}
	return m
}

type submitResultMsg struct {
	out open.Outcome
	err error
}

func (m *Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.promptArea.SetWidth(m.width - 4)
		return m, nil

	case submitResultMsg:
		m.running = false
		if msg.err != nil {
			m.err = msg.err
			m.status = ""
			return m, nil
		}
		m.result = msg.out
		m.done = true
		m.quitting = true
		return m, tea.Quit

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleMouse hit-tests a click against the regions the last render recorded.
// A submission in flight ignores clicks the same way handleKey ignores keys:
// nothing should race the in-flight request's reads of these fields.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if m.running {
		return m, nil
	}

	switch {
	case msg.Y == m.buttonsRow && msg.X >= m.createButtonX0 && msg.X <= m.createButtonX1:
		m.running = true
		m.err = nil
		m.status = "working..."
		return m, m.submitCmd()

	case msg.Y == m.buttonsRow && msg.X >= m.cancelButtonX0 && msg.X <= m.cancelButtonX1:
		m.quitting = true
		return m, tea.Quit

	case msg.Y == m.linkRow:
		m.setFocus(fieldLink)
		return m, nil

	case msg.Y == m.toggleRow:
		// The whole row flips it, not just the checkbox glyph -- a bigger
		// target is the point of a click over a keystroke.
		m.agentOn = !m.agentOn
		m.setFocus(fieldToggle)
		return m, nil

	case msg.Y >= m.promptTop && msg.Y <= m.promptBot:
		m.setFocus(fieldPrompt)
		return m, nil
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.running {
		// A submission is in flight: only allow bailing out, nothing that
		// would race the in-flight request's reads of these fields.
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "esc":
		m.quitting = true
		return m, tea.Quit

	case "ctrl+s":
		m.running = true
		m.err = nil
		m.status = "working..."
		return m, m.submitCmd()

	case "tab":
		m.setFocus(nextFocus(m.focus, true))
		return m, nil
	case "shift+tab":
		m.setFocus(nextFocus(m.focus, false))
		return m, nil
	}

	switch m.focus {
	case fieldToggle:
		if msg.String() == " " || msg.String() == "enter" {
			m.agentOn = !m.agentOn
		}
		return m, nil

	case fieldLink:
		var cmd tea.Cmd
		before := m.linkInput.Value()
		m.linkInput, cmd = m.linkInput.Update(msg)
		if m.linkInput.Value() != before {
			m.onLinkChanged()
		}
		return m, cmd

	default: // fieldPrompt
		var cmd tea.Cmd
		before := m.promptArea.Value()
		m.promptArea, cmd = m.promptArea.Update(msg)
		if m.promptArea.Value() != before {
			m.promptEdited = true
		}
		return m, cmd
	}
}

func (m *Model) setFocus(f field) {
	m.focus = f
	if f == fieldLink {
		m.linkInput.Focus()
	} else {
		m.linkInput.Blur()
	}
	if f == fieldPrompt {
		m.promptArea.Focus()
	} else {
		m.promptArea.Blur()
	}
}

// onLinkChanged re-parses the target and, unless the user has taken over the
// prompt box themselves, refreshes the preview to match.
func (m *Model) onLinkChanged() {
	m.tgt = target.Parse(m.linkInput.Value())
	if m.promptEdited {
		return
	}
	preview, err := open.PreviewPrompt(m.cfg, m.tgt)
	if err != nil {
		// A broken template is a config problem, not something retyping the
		// link will fix, so it is surfaced rather than silently kept stale.
		m.err = err
		return
	}
	m.promptArea.SetValue(preview)
}

// buildRunOptions decides what Run should be told, kept as a pure function so
// the "edited text wins outright" rule is testable without a live component.
func buildRunOptions(agentOn, promptEdited bool, promptText string) open.Options {
	agent := agentOn
	opts := open.Options{Agent: &agent}
	if promptEdited {
		opts.Prompt = promptText
	}
	return opts
}

// submitCmd closes over exactly what is on screen right now and runs it on a
// worker goroutine, the standard bubbletea way to do blocking work without
// freezing the UI.
func (m *Model) submitCmd() tea.Cmd {
	input := m.linkInput.Value()
	opts := buildRunOptions(m.agentOn, m.promptEdited, m.promptArea.Value())
	deps := m.deps
	cfg := m.cfg

	return func() tea.Msg {
		out, err := open.Run(deps, cfg, input, opts)
		return submitResultMsg{out: out, err: err}
	}
}

// Result reports what the popup decided, for the caller to notify or log
// once the Program has exited normally rather than been cancelled.
func (m *Model) Result() (open.Outcome, error, bool) {
	return m.result, m.err, m.done
}

func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	return m.view()
}

func targetSummary(t target.Target) string {
	switch t.Kind {
	case target.KindGitHubPR:
		return fmt.Sprintf("GitHub PR: %s/%s #%d", t.Owner, t.Repo, t.Number)
	case target.KindLinear:
		return fmt.Sprintf("Linear issue: %s", t.Issue)
	default:
		if t.Text == "" {
			return "New Space"
		}
		return fmt.Sprintf("New Space: %q", t.Text)
	}
}
