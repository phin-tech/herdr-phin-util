package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/session"
)

func promptPicker(t *testing.T, candidates ...session.Candidate) *Picker {
	t.Helper()
	cfg := &config.Settings{
		Agent:   config.AgentSettings{Enabled: true, Kind: "claude"},
		Prompts: config.PromptSettings{GithubPR: "Review PR #{{.Number}}", Project: "in {{.Text}}"},
	}
	return NewPicker(cfg, session.Deps{}, nil, candidates).WithWorkspaces(nil)
}

func TestPromptEditorOpensPrefilledForALink(t *testing.T) {
	p := promptPicker(t)
	typeInto(p, prURL)

	p.Update(tea.KeyMsg{Type: tea.KeyCtrlE})

	if !p.editing {
		t.Fatal("ctrl+e should open the prompt editor")
	}
	if got := p.promptArea.Value(); got != "Review PR #42" {
		t.Errorf("prompt = %q, want it rendered from the template", got)
	}
	if !strings.Contains(p.View(), "Review PR #42") {
		t.Error("the editor should be on screen")
	}
}

// Switching to a Space starts no agent, so there is no prompt to edit.
func TestPromptEditorRefusesForASpace(t *testing.T) {
	p := promptPicker(t, space("running", "/src/running"))

	p.Update(tea.KeyMsg{Type: tea.KeyCtrlE})

	if p.editing {
		t.Error("a Space row has no agent step and so no prompt")
	}
}

func TestPromptEditorClosesAndKeepsText(t *testing.T) {
	p := promptPicker(t)
	typeInto(p, prURL)
	p.Update(tea.KeyMsg{Type: tea.KeyCtrlE})

	// Type into the prompt box.
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	if !p.promptEdited {
		t.Fatal("typing in the box should mark the prompt edited")
	}
	edited := p.promptArea.Value()

	p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if p.editing {
		t.Error("esc should close the editor")
	}
	if p.quitting {
		t.Error("esc in the editor must not quit the popup")
	}
	if p.promptArea.Value() != edited {
		t.Error("closing the editor should keep the text")
	}
}

// While the editor is open the list must not react to keys.
func TestPromptEditorOwnsTheKeyboard(t *testing.T) {
	p := promptPicker(t, project("a", "/a"), project("b", "/b"))
	p.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if !p.editing {
		t.Fatal("expected the editor to open for a project row")
	}

	before := p.cursor
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.cursor != before {
		t.Error("the list should not move while the prompt box has focus")
	}

	// Printable keys go to the prompt, not the filter.
	filterBefore := p.filter.Value()
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if p.filter.Value() != filterBefore {
		t.Error("typing should go to the prompt, not the filter")
	}
}

// Edited text wins outright over the template, the maker's rule.
func TestEditedPromptIsPassedToTheRun(t *testing.T) {
	p := promptPicker(t)
	typeInto(p, prURL)
	p.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	p.promptArea.SetValue("my own words")
	p.promptEdited = true

	// submit closes over what is on screen; inspect that rather than running it.
	if cmd := p.submit(); cmd == nil {
		t.Fatal("expected a submit command")
	}
	if !p.running {
		t.Error("submitting should enter the running state")
	}
}

// The hint only advertises the editor where it would do something.
func TestPromptHintOnlyWhenAnAgentWouldStart(t *testing.T) {
	withAgent := promptPicker(t, project("a", "/a"))
	if !strings.Contains(withAgent.viewPickerHint(), "prompt") {
		t.Error("a project row starts an agent, so the hint should offer the prompt")
	}

	onlySpace := promptPicker(t, space("running", "/src/running"))
	if strings.Contains(onlySpace.viewPickerHint(), "prompt") {
		t.Error("a Space row should not advertise a prompt editor")
	}

	// Nor when the agent toggle is off.
	off := promptPicker(t, project("a", "/a"))
	off.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if strings.Contains(off.viewPickerHint(), "prompt") {
		t.Error("with the agent off there is no prompt to write")
	}
}

func TestPromptEditorAlsoWorksForAnOpenLinkRow(t *testing.T) {
	// A link that already has a Space is a switch, so no prompt.
	p := NewPicker(
		&config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}},
		session.Deps{}, nil, nil,
	).WithWorkspaces([]herdr.Workspace{{WorkspaceID: "w7", Label: "roux#42"}})
	typeInto(p, prURL)

	p.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if p.editing {
		t.Error("a link that resolves to an open Space starts no agent")
	}
}
