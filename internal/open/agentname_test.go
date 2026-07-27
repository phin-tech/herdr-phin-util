package open

import (
	"regexp"
	"strings"
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

func TestAgentNameInQualifiesBySpace(t *testing.T) {
	if got := agentNameIn("codex-reviewer-3", "w14"); got != "codex-reviewer-3-w14" {
		t.Errorf("got %q, want codex-reviewer-3-w14", got)
	}
	// Nothing to qualify with, so nothing is invented.
	if got := agentNameIn("codex-reviewer-3", ""); got != "codex-reviewer-3" {
		t.Errorf("got %q, want the name unchanged", got)
	}
}

// The suffix is the part that makes the name unique, so the 32-character cap
// comes out of the base -- and the result still has to be a name the server
// accepts.
func TestAgentNameInTrimsTheBaseNotTheSuffix(t *testing.T) {
	got := agentNameIn(agentName("a-really-quite-unreasonably-long-name"), "w14")

	if len(got) > agentNameMaxLen {
		t.Errorf("got %q (%d chars), want at most %d", got, len(got), agentNameMaxLen)
	}
	if !strings.HasSuffix(got, "-w14") {
		t.Errorf("got %q, want it to keep the -w14 suffix", got)
	}
	if !valid.MatchString(got) {
		t.Errorf("%q would be rejected", got)
	}
	if strings.Contains(got, "--") {
		t.Errorf("got %q, want no separator run where the base was cut", got)
	}
}

// A Space id is not promised to be name-safe, and a suffix that made the name
// invalid would trade one failure for another.
func TestAgentNameInSanitisesTheSpaceID(t *testing.T) {
	got := agentNameIn("reviewer", "W 14/b")
	if got != "reviewer-w-14-b" {
		t.Errorf("got %q, want reviewer-w-14-b", got)
	}
	if !valid.MatchString(got) {
		t.Errorf("%q would be rejected", got)
	}
}

// The single-agent path collides just as readily as a setup does: the same PR
// opened twice derives the same name from the same label.
func TestRunRetriesATakenAgentNameQualifiedByTheSpace(t *testing.T) {
	s := &fakeSession{
		pane:        herdr.Pane{PaneID: "w14:p1"},
		workspaceID: "w14",
		takenNames:  map[string]bool{"scratch-space-1": true},
	}
	cfg := &config.Settings{
		Agent:   config.AgentSettings{Enabled: true, Kind: "claude"},
		Prompts: config.PromptSettings{Plain: "hello"},
	}

	if _, err := Run(Deps{Session: s}, cfg, "Scratch Space #1", Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(s.startAgentCalls) != 2 {
		t.Fatalf("agent.start calls = %+v, want 2", s.startAgentCalls)
	}
	if got := s.startAgentCalls[1].name; got != "scratch-space-1-w14" {
		t.Errorf("retried under %q, want scratch-space-1-w14", got)
	}
	if len(s.sendTextCalls) != 1 {
		t.Error("the prompt should still be typed once the agent starts")
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
