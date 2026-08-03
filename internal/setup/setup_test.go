package setup

import (
	"strings"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/target"
)

func TestMatches(t *testing.T) {
	pr := Subject{Kind: target.KindGitHubPR, Owner: "phin-tech", Repo: "roux", RepoName: "roux", Branch: "fix/thing"}

	tests := []struct {
		name  string
		setup Setup
		sub   Subject
		want  bool
	}{
		{"bare setup matches anything", Setup{}, pr, true},
		{"kind matches", Setup{AppliesTo: []string{"github_pr"}}, pr, true},
		{"kind does not match", Setup{AppliesTo: []string{"project"}}, pr, false},
		{"one of several kinds", Setup{AppliesTo: []string{"project", "github_pr"}}, pr, true},
		{"repo by owner/repo", Setup{Repos: []string{"phin-tech/roux"}}, pr, true},
		{"repo by bare name", Setup{Repos: []string{"roux"}}, pr, true},
		{"repo by glob", Setup{Repos: []string{"phin-tech/*"}}, pr, true},
		{"repo does not match", Setup{Repos: []string{"other/*"}}, pr, false},
		{"branch glob", Setup{Branches: []string{"fix/*"}}, pr, true},
		{"branch does not match", Setup{Branches: []string{"main"}}, pr, false},
		{"scoped dir matches its repo", Setup{ScopedRepo: "roux"}, pr, true},
		{"scoped dir with owner", Setup{ScopedRepo: "phin-tech/roux"}, pr, true},
		{"scoped dir for another repo", Setup{ScopedRepo: "hearth-mud"}, pr, false},
		{
			// A worktree directory is named for its branch, not the repo, so the
			// checkout's basename has to be matchable on its own.
			"scoped dir matches the checkout directory name",
			Setup{ScopedRepo: "roux-fix-thing"},
			Subject{Repo: "roux", RepoName: "roux-fix-thing"},
			true,
		},
		{
			"branch filter with no branch resolved",
			Setup{Branches: []string{"main"}},
			Subject{Kind: target.KindProject, Repo: "roux"},
			false,
		},
		{
			// A picker row that has not resolved a kind yet must not have every
			// kind-specific setup hidden from it.
			"unknown kind is not filtered out",
			Setup{AppliesTo: []string{"github_pr"}},
			Subject{Repo: "roux"},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.setup.Matches(tt.sub); got != tt.want {
				t.Errorf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		setup Setup
		want  string
	}{
		{"no name", Setup{Tabs: []Tab{{Name: "a"}}}, "no name"},
		{"no tabs", Setup{Name: "x"}, "no tabs"},
		{
			"unknown kind",
			Setup{Name: "x", AppliesTo: []string{"github_pull_request"}, Tabs: []Tab{{Name: "a"}}},
			"is not a target kind",
		},
		{
			"tab with both command and panes",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", Command: "ls", Panes: []Pane{{}}}}},
			"both a command and panes",
		},
		{
			"first pane splitting",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{Split: "down"}}}}},
			"cannot split",
		},
		{
			"bad split direction",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{}, {Split: "sideways"}}}}},
			`is not "right" or "down"`,
		},
		{
			"ratio out of range",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{Ratio: 1.5}}}}},
			"is not between 0 and 1",
		},
		{
			"prompt without an agent",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{Prompt: "hi"}}}}},
			"no agent to type it into",
		},
		{
			"agent and command together",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{Agent: "claude", Command: "ls"}}}}},
			"both an agent and a command",
		},
		{
			"prompt and skill together",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{Agent: "claude", Prompt: "hi", Skill: "/x"}}}}},
			"both a prompt and a skill",
		},
		{
			"wait_for with no match",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{WaitFor: &WaitFor{TimeoutMs: 10}}}}}},
			"no match to wait for",
		},
		{
			"two focused panes",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{Focus: true}, {Split: "down", Focus: true}}}}},
			"only one can be",
		},
		{
			"focus inside a for_each tab",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", ForEach: "layers", Panes: []Pane{{Focus: true}}}}},
			"focus true inside a for_each tab",
		},
		{
			"as without for_each",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", As: "layer", Panes: []Pane{{}}}}},
			`as "layer" is set without a for_each`,
		},
		{
			"for_each with nothing after it",
			Setup{Name: "x", Tabs: []Tab{{Name: "a", ForEach: "   ", Panes: []Pane{{}}}}},
			"for_each has nothing after it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(tt.setup.Validate(), "\n")
			if !strings.Contains(got, tt.want) {
				t.Errorf("Validate() = %q, want it to mention %q", got, tt.want)
			}
		})
	}
}

func TestValidateAcceptsAWholeRealSetup(t *testing.T) {
	s := Setup{
		Name:      "pr-review",
		AppliesTo: []string{"github_pr"},
		Tabs: []Tab{
			{Name: "review", Panes: []Pane{
				{Agent: "claude", Prompt: "review {{.Number}}", Focus: true},
				{Split: "right", Agent: "claude", Skill: "/code-review", Submit: true},
				{Split: "down", Ratio: 0.25, Command: "roborev review-branch", WaitFor: &WaitFor{Match: "queued"}},
			}},
			{Name: "shell"},
		},
	}
	if problems := s.Validate(); len(problems) != 0 {
		t.Errorf("Validate() = %v, want none", problems)
	}
}

func TestEffectivePanes(t *testing.T) {
	if got := (Tab{Command: "lazygit"}).EffectivePanes(); len(got) != 1 || got[0].Command != "lazygit" {
		t.Errorf("command shorthand became %+v", got)
	}
	if got := (Tab{}).EffectivePanes(); len(got) != 1 || got[0].Command != "" {
		t.Errorf("bare tab became %+v, want one empty shell", got)
	}
	if got := (Tab{Panes: []Pane{{}, {}}}).EffectivePanes(); len(got) != 2 {
		t.Errorf("explicit panes became %+v", got)
	}
}

func TestFind(t *testing.T) {
	setups := []Setup{{Name: "one"}, {Name: "Two"}}
	if _, ok := Find(setups, "TWO"); !ok {
		t.Error("Find is case sensitive, but a name typed at a CLI will not be")
	}
	if _, ok := Find(setups, "three"); ok {
		t.Error("found a setup that does not exist")
	}
}

// model and args are the agent's command line, so a pane with no agent has
// nowhere to put them -- and a command pane spells its own flags out anyway.
func TestValidateRejectsModelAndArgsWithoutAnAgent(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{
		{Command: "make", Model: "opus"},
		{Split: "down", Command: "make", Args: []string{"--flag"}},
	}}}}

	problems := strings.Join(s.Validate(), "\n")
	if !strings.Contains(problems, "model") || !strings.Contains(problems, "args") {
		t.Errorf("problems = %q, want both named", problems)
	}
}
