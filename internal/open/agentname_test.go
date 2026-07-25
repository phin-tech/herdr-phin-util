package open

import (
	"regexp"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
)

// valid mirrors what agent.start actually enforces, confirmed against a live
// server: a lowercase letter first, then lowercase letters, digits, '-' or
// '_', 1 to 32 characters.
var valid = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func TestAgentNameAcceptsEveryLabelThisPluginProduces(t *testing.T) {
	cases := []struct{ label, want string }{
		// The one that was reported: a pull request Space.
		{"roux#42", "roux-42"},
		{"herdr-phin-util#7", "herdr-phin-util-7"},
		// A Linear label is upper case, which is rejected on its own.
		{"ENG-123", "eng-123"},
		// A worktree Space is "repo/branch".
		{"roux/feature", "roux-feature"},
		{"roux-next-gen/codex/iterm-split", "roux-next-gen-codex-iterm-split"},
		// A plain Space is whatever was typed.
		{"scratch space", "scratch-space"},
		{"My Notes!", "my-notes"},
		// A project Space is a directory name, usually already fine.
		{"herdr-phin-util", "herdr-phin-util"},
		{"dotfiles", "dotfiles"},
	}

	for _, tc := range cases {
		got := agentName(tc.label)
		if got != tc.want {
			t.Errorf("agentName(%q) = %q, want %q", tc.label, got, tc.want)
		}
		if !valid.MatchString(got) {
			t.Errorf("agentName(%q) = %q, which agent.start would reject", tc.label, got)
		}
	}
}

// A name has to begin with a letter, but throwing away the leading digits
// would rename the thing: 2fa-service is not "fa-service".
func TestAgentNameKeepsLeadingDigitsBehindAPrefix(t *testing.T) {
	got := agentName("2fa-service")
	if got != "agent-2fa-service" {
		t.Errorf("got %q, want agent-2fa-service", got)
	}
	if !valid.MatchString(got) {
		t.Errorf("%q would be rejected", got)
	}
}

func TestAgentNameTruncatesToTheLimit(t *testing.T) {
	long := "a-really-quite-unreasonably-long-repository-name/with-a-branch"
	got := agentName(long)

	if len(got) > agentNameMaxLen {
		t.Errorf("got %q (%d chars), want at most %d", got, len(got), agentNameMaxLen)
	}
	if !valid.MatchString(got) {
		t.Errorf("%q would be rejected", got)
	}
	// Truncation must not leave a trailing separator.
	if got[len(got)-1] == '-' || got[len(got)-1] == '_' {
		t.Errorf("got %q, want no trailing separator", got)
	}
}

func TestAgentNameFallsBackWhenNothingSurvives(t *testing.T) {
	for _, label := range []string{"", "   ", "###", "!!!", "---", "___"} {
		got := agentName(label)
		if got != agentNameFallback {
			t.Errorf("agentName(%q) = %q, want %q", label, got, agentNameFallback)
		}
		if !valid.MatchString(got) {
			t.Errorf("%q would be rejected", got)
		}
	}
}

func TestAgentNameCollapsesSeparatorRuns(t *testing.T) {
	if got := agentName("a / b"); got != "a-b" {
		t.Errorf("got %q, want a-b", got)
	}
	if got := agentName("roux  #  42"); got != "roux-42" {
		t.Errorf("got %q, want roux-42", got)
	}
}

// The end-to-end guard: whatever a Space is called, the name handed to
// agent.start is one the server accepts.
func TestAgentStartReceivesASanitisedName(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "w1:p1"}, workspaceID: "w1"}
	cfg := &config.Settings{
		Agent:   config.AgentSettings{Enabled: true, Kind: "claude"},
		Prompts: config.PromptSettings{Plain: "hello"},
	}

	// A plain target, so no repo resolution or network is involved.
	if _, err := Run(Deps{Session: s}, cfg, "Scratch Space #1", Options{}); err != nil {
		t.Fatal(err)
	}

	if len(s.startAgentCalls) != 1 {
		t.Fatalf("expected one agent.start, got %d", len(s.startAgentCalls))
	}
	got := s.startAgentCalls[0].name
	if !valid.MatchString(got) {
		t.Errorf("agent.start got name %q, which the server would reject", got)
	}
	if got != "scratch-space-1" {
		t.Errorf("name = %q, want scratch-space-1", got)
	}
}

// The Space itself keeps its readable label -- only the agent name is
// constrained.
func TestSanitisingTheAgentNameLeavesTheLabelAlone(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "w1:p1"}, workspaceID: "w1"}
	cfg := &config.Settings{
		Agent:   config.AgentSettings{Enabled: true, Kind: "claude"},
		Prompts: config.PromptSettings{Plain: "hello"},
	}

	out, err := Run(Deps{Session: s}, cfg, "Scratch Space #1", Options{})
	if err != nil {
		t.Fatal(err)
	}

	if out.Label != "Scratch Space #1" {
		t.Errorf("Label = %q, want the text as typed", out.Label)
	}
	if got := s.createWorkspaceCalls[0].label; got != "Scratch Space #1" {
		t.Errorf("workspace.create label = %q, want the text as typed", got)
	}
}
