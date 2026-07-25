package ui

import (
	"errors"
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/gh"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/open"
)

// Keeps the popup's decisions from being swayed by whatever plugin action
// happens to be invoking the test binary.
func TestMain(m *testing.M) {
	os.Unsetenv("HERDR_PLUGIN_CONTEXT_JSON")
	os.Exit(m.Run())
}

type fakeSession struct {
	pane        herdr.Pane
	workspaceID string
	err         error

	lastRequest                        herdr.WorktreeRequest
	sawWorktree                        bool
	sawWorkspace                       bool
	sawWorkspaceCwd, sawWorkspaceLabel string

	startAgentCalls int
	sendTextCalls   []string
}

func (f *fakeSession) CreateWorktree(req herdr.WorktreeRequest) (herdr.Pane, string, error) {
	f.sawWorktree = true
	f.lastRequest = req
	if f.err != nil {
		return herdr.Pane{}, "", f.err
	}
	return f.pane, f.workspaceID, nil
}
func (f *fakeSession) OpenWorktree(req herdr.WorktreeRequest) (herdr.Pane, string, error) {
	return f.pane, f.workspaceID, f.err
}
func (f *fakeSession) CreateWorkspace(cwd, label string, focus bool) (herdr.Pane, string, error) {
	f.sawWorkspace = true
	f.sawWorkspaceCwd, f.sawWorkspaceLabel = cwd, label
	if f.err != nil {
		return herdr.Pane{}, "", f.err
	}
	return f.pane, f.workspaceID, nil
}
func (f *fakeSession) StartAgent(paneID, name, kind string) error {
	f.startAgentCalls++
	return nil
}
func (f *fakeSession) WaitAgentIdle(paneID string) error                        { return nil }
func (f *fakeSession) WaitPaneOutput(paneID, value string, timeoutMs int) error { return nil }
func (f *fakeSession) SendText(paneID, text string) error {
	f.sendTextCalls = append(f.sendTextCalls, text)
	return nil
}

func (f *fakeSession) FetchBranch(repoPath, branch string) error { return nil }

func (f *fakeSession) LookupIssue(owner, repo string, number int) (gh.IssueInfo, error) {
	return gh.IssueInfo{}, nil
}

func (f *fakeSession) LookupPR(owner, repo string, number int) (gh.PRInfo, error) {
	return gh.PRInfo{}, errors.New("not used in these tests")
}

func testConfig() *config.Settings {
	return &config.Settings{
		Agent: config.AgentSettings{Enabled: true, Kind: "claude"},
		Prompts: config.PromptSettings{
			GithubPR: "PR #{{.Number}}",
			Linear:   "Issue {{.Issue}}",
			Plain:    "{{.Text}}",
		},
	}
}

func newTestModel() (*Model, *fakeSession) {
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}
	deps := open.Deps{Session: sess, PRs: sess, Git: sess, Cwd: "/tmp/cwd"}
	m := New(testConfig(), deps)
	return m, sess
}

func TestNewModelDefaultsAgentToggleFromConfig(t *testing.T) {
	m, _ := newTestModel()
	if !m.agentOn {
		t.Error("agentOn should default to the config's Agent.Enabled")
	}
}

func TestTypingLinkUpdatesLivePreviewUntilPromptEdited(t *testing.T) {
	m, _ := newTestModel()
	m = typeString(m, "https://linear.app/phin/issue/ENG-9/do-a-thing")
	if want := "Issue ENG-9"; m.promptArea.Value() != want {
		t.Errorf("preview = %q, want %q", m.promptArea.Value(), want)
	}

	// Editing the prompt directly must stop the auto-fill from clobbering it.
	m.focus = fieldPrompt
	m.promptArea.Focus()
	m, _ = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	if !m.promptEdited {
		t.Fatal("editing the prompt field should set promptEdited")
	}
	edited := m.promptArea.Value()

	m.focus = fieldLink
	m = typeString(m, " more")
	if m.promptArea.Value() != edited {
		t.Errorf("prompt changed after being manually edited: got %q, want %q", m.promptArea.Value(), edited)
	}
}

func TestTabCyclesFocusForwardAndBackward(t *testing.T) {
	if got := nextFocus(fieldLink, true); got != fieldToggle {
		t.Errorf("forward from link = %v, want toggle", got)
	}
	if got := nextFocus(fieldToggle, true); got != fieldPrompt {
		t.Errorf("forward from toggle = %v, want prompt", got)
	}
	if got := nextFocus(fieldPrompt, true); got != fieldLink {
		t.Errorf("forward from prompt = %v, want link (wraps)", got)
	}
	if got := nextFocus(fieldLink, false); got != fieldPrompt {
		t.Errorf("backward from link = %v, want prompt (wraps)", got)
	}
}

func TestSpaceTogglesAgentWhenToggleFocused(t *testing.T) {
	m, _ := newTestModel()
	m.focus = fieldToggle
	m.agentOn = true

	m, _ = sendKey(m, spaceAwareKey(' '))
	if m.agentOn {
		t.Error("space should have flipped agentOn to false")
	}
	m, _ = sendKey(m, spaceAwareKey(' '))
	if !m.agentOn {
		t.Error("space should have flipped agentOn back to true")
	}
}

// A space typed into the link field must reach the text input, not be
// swallowed as if it were the toggle's key.
func TestSpaceInLinkFieldIsNotTreatedAsToggle(t *testing.T) {
	m, _ := newTestModel()
	m.focus = fieldLink
	m.agentOn = true
	m = typeString(m, "a b")
	if !m.agentOn {
		t.Error("agentOn should be untouched while typing in the link field")
	}
	if m.linkInput.Value() != "a b" {
		t.Errorf("linkInput = %q, want %q", m.linkInput.Value(), "a b")
	}
}

func TestBuildRunOptionsUsesOverrideOnlyWhenPromptEdited(t *testing.T) {
	opts := buildRunOptions(true, false, "whatever is on screen")
	if opts.Agent == nil || !*opts.Agent {
		t.Error("Agent should reflect the toggle")
	}
	if opts.Prompt != "" {
		t.Error("Prompt override should be empty when the user never edited it")
	}

	opts = buildRunOptions(false, true, "edited text")
	if opts.Agent == nil || *opts.Agent {
		t.Error("Agent should reflect the toggle")
	}
	if opts.Prompt != "edited text" {
		t.Errorf("Prompt = %q, want the edited text", opts.Prompt)
	}
}

// submitCmd is what Enter/ctrl+s produces: a tea.Cmd, callable directly in a
// test without a running Program, closing over exactly what was on screen.
func TestSubmitCmdRunsWithCurrentFieldValues(t *testing.T) {
	m, sess := newTestModel()
	m = typeString(m, "a plain space name")
	m.agentOn = false

	cmd := m.submitCmd()
	msg := cmd()
	res, ok := msg.(submitResultMsg)
	if !ok {
		t.Fatalf("submitCmd produced %T, want submitResultMsg", msg)
	}
	if res.err != nil {
		t.Fatalf("submit: %v", res.err)
	}
	if !sess.sawWorkspace {
		t.Error("a plain target should create a workspace")
	}
	if sess.sawWorkspaceLabel != "a plain space name" {
		t.Errorf("label = %q", sess.sawWorkspaceLabel)
	}
	if sess.startAgentCalls != 0 {
		t.Error("agent was toggled off and should not have started")
	}
}

// --- mouse ---

func click(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

// Hit regions are only known once a frame has been drawn -- exactly like a
// real terminal, where nothing is clickable before it is first rendered.
func rendered(m *Model) *Model {
	m.View()
	return m
}

func TestClickingLinkRowFocusesLinkField(t *testing.T) {
	m, _ := newTestModel()
	m.setFocus(fieldPrompt)
	m = rendered(m)

	m, _ = sendMouse(m, click(2, m.linkRow))
	if m.focus != fieldLink {
		t.Errorf("focus = %v, want fieldLink", m.focus)
	}
}

func TestClickingToggleRowFlipsAgentAndFocusesIt(t *testing.T) {
	m, _ := newTestModel()
	m.agentOn = true
	m = rendered(m)

	m, _ = sendMouse(m, click(2, m.toggleRow))
	if m.agentOn {
		t.Error("clicking the toggle row should have flipped agentOn off")
	}
	if m.focus != fieldToggle {
		t.Errorf("focus = %v, want fieldToggle", m.focus)
	}

	m, _ = sendMouse(m, click(2, m.toggleRow))
	if !m.agentOn {
		t.Error("a second click should flip it back on")
	}
}

func TestClickingPromptAreaFocusesPrompt(t *testing.T) {
	m, _ := newTestModel()
	m = rendered(m)

	m, _ = sendMouse(m, click(2, m.promptTop))
	if m.focus != fieldPrompt {
		t.Errorf("focus = %v, want fieldPrompt", m.focus)
	}
}

func TestClickingCreateButtonSubmits(t *testing.T) {
	m, sess := newTestModel()
	m = typeString(m, "a plain space name")
	m.agentOn = false
	m = rendered(m)

	_, cmd := sendMouse(m, click(m.createButtonX0, m.buttonsRow))
	if cmd == nil {
		t.Fatal("clicking Create should produce a command")
	}
	msg := cmd()
	res, ok := msg.(submitResultMsg)
	if !ok {
		t.Fatalf("clicking Create produced %T, want submitResultMsg", msg)
	}
	if res.err != nil {
		t.Fatalf("submit: %v", res.err)
	}
	if !sess.sawWorkspace {
		t.Error("Create should have run the submission")
	}
}

func TestClickingCancelButtonQuits(t *testing.T) {
	m, _ := newTestModel()
	m = rendered(m)

	m, cmd := sendMouse(m, click(m.cancelButtonX0, m.buttonsRow))
	if !m.quitting {
		t.Error("clicking Cancel should quit")
	}
	if cmd == nil {
		t.Error("clicking Cancel should return tea.Quit")
	}
}

// A click outside any known region -- off in the margins -- must be inert
// rather than accidentally landing on whatever region happens to be nearest.
func TestClickOutsideAnyRegionIsIgnored(t *testing.T) {
	m, _ := newTestModel()
	m.setFocus(fieldLink)
	m = rendered(m)

	m, cmd := sendMouse(m, click(0, 999))
	if m.focus != fieldLink {
		t.Errorf("focus changed to %v after a click on nothing", m.focus)
	}
	if cmd != nil {
		t.Error("a click on nothing should not produce a command")
	}
}

func sendMouse(m *Model, msg tea.MouseMsg) (*Model, tea.Cmd) {
	updated, cmd := m.Update(msg)
	return updated.(*Model), cmd
}

func typeString(m *Model, s string) *Model {
	m.focus = fieldLink
	for _, r := range s {
		m, _ = sendKey(m, spaceAwareKey(r))
	}
	return m
}

// spaceAwareKey mirrors bubbletea's real input parser: a KeySpace event still
// carries Runes: [' '], only its Type differs from KeyRunes. Bubbles'
// textinput reads Runes regardless of Type, so a synthetic event missing that
// field silently drops the character.
func spaceAwareKey(r rune) tea.KeyMsg {
	if r == ' ' {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func sendKey(m *Model, msg tea.KeyMsg) (*Model, tea.Cmd) {
	updated, cmd := m.Update(msg)
	return updated.(*Model), cmd
}
