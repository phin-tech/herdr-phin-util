package open

import (
	"errors"
	"strings"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/gh"
	"github.com/phin-tech/herdr-phin-util/internal/setup"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// sampleStack is a resolved three-layer stack, bottom first, as gh.Stack
// would return it -- used across the tests below so they agree on what a
// "real" stack looks like.
func sampleStack() []gh.StackPR {
	return []gh.StackPR{
		{Number: 100, Title: "bottom", URL: "https://github.com/o/r/pull/100", HeadBranch: "b100", BaseBranch: "main", HeadSHA: "sha100"},
		{Number: 101, Title: "middle", URL: "https://github.com/o/r/pull/101", HeadBranch: "b101", BaseBranch: "b100", HeadSHA: "sha101"},
		{Number: 102, Title: "top", URL: "https://github.com/o/r/pull/102", HeadBranch: "b102", BaseBranch: "b101", HeadSHA: "sha102"},
	}
}

func TestStackLayersFields(t *testing.T) {
	got := stackLayers(sampleStack())
	if len(got) != 3 {
		t.Fatalf("stackLayers len = %d, want 3", len(got))
	}
	bottom, middle, top := got[0], got[1], got[2]

	// The bottom layer bases on the trunk, not on another open PR, so its
	// base_pr is empty -- there is no PR number to put there.
	if bottom["layer"] != "1" || bottom["pr"] != "100" || bottom["title"] != "bottom" ||
		bottom["url"] != "https://github.com/o/r/pull/100" || bottom["head_branch"] != "b100" ||
		bottom["head_sha"] != "sha100" || bottom["base_branch"] != "main" || bottom["base_pr"] != "" {
		t.Errorf("bottom layer = %+v", bottom)
	}
	if middle["layer"] != "2" || middle["pr"] != "101" || middle["base_pr"] != "100" {
		t.Errorf("middle layer = %+v", middle)
	}
	if top["layer"] != "3" || top["pr"] != "102" || top["base_pr"] != "101" {
		t.Errorf("top layer = %+v", top)
	}
}

func TestResolveListsSkipsNonGithubPRTarget(t *testing.T) {
	prs := &fakePRLookup{stack: sampleStack()}
	lists, err := resolveLists(prs, target.Target{Kind: target.KindLinear}, []string{"layers"})
	if err != nil {
		t.Fatalf("resolveLists: %v", err)
	}
	if lists != nil {
		t.Errorf("lists = %v, want nil for a non-github_pr target", lists)
	}
	if prs.stackCalls != 0 {
		t.Errorf("Stack called %d times, want 0", prs.stackCalls)
	}
}

// The whole point of resolving lazily: a setup that never names "layers"
// must never trigger a gh pr list call, even against a github_pr target.
func TestResolveListsSkipsWhenNobodyAskedForLayers(t *testing.T) {
	prs := &fakePRLookup{stack: sampleStack()}
	tgt := target.Target{Kind: target.KindGitHubPR, Owner: "o", Repo: "r", Number: 101}
	lists, err := resolveLists(prs, tgt, []string{"something_else"})
	if err != nil {
		t.Fatalf("resolveLists: %v", err)
	}
	if lists != nil {
		t.Errorf("lists = %v, want nil", lists)
	}
	if prs.stackCalls != 0 {
		t.Errorf("Stack called %d times, want 0 -- gh pr list is a network call and must stay lazy", prs.stackCalls)
	}
}

func TestResolveListsFetchesLayersWhenNamed(t *testing.T) {
	prs := &fakePRLookup{stack: sampleStack()}
	tgt := target.Target{Kind: target.KindGitHubPR, Owner: "o", Repo: "r", Number: 101}
	lists, err := resolveLists(prs, tgt, []string{"layers"})
	if err != nil {
		t.Fatalf("resolveLists: %v", err)
	}
	if prs.stackCalls != 1 {
		t.Errorf("Stack called %d times, want 1", prs.stackCalls)
	}
	if len(lists["layers"]) != 3 {
		t.Errorf("lists[\"layers\"] = %v, want 3 elements", lists["layers"])
	}
}

// A setup's for_each names are resolved regardless of which order they
// appear in, and asking for a name alongside "layers" must not suppress the
// gh call.
func TestResolveListsFetchesLayersAmongOtherNames(t *testing.T) {
	prs := &fakePRLookup{stack: sampleStack()}
	tgt := target.Target{Kind: target.KindGitHubPR, Owner: "o", Repo: "r", Number: 101}
	lists, err := resolveLists(prs, tgt, []string{"packages", "layers"})
	if err != nil {
		t.Fatalf("resolveLists: %v", err)
	}
	if prs.stackCalls != 1 {
		t.Errorf("Stack called %d times, want 1", prs.stackCalls)
	}
	if len(lists) != 1 {
		t.Errorf("lists = %v, want only \"layers\" (no source for \"packages\" exists)", lists)
	}
}

// gh failing to resolve a stack is a resolve failure like any other: an
// error, not a partially-populated Lists.
func TestResolveListsPropagatesGhFailure(t *testing.T) {
	prs := &fakePRLookup{stackErr: errors.New("gh: rate limited")}
	tgt := target.Target{Kind: target.KindGitHubPR, Owner: "o", Repo: "r", Number: 1}
	if _, err := resolveLists(prs, tgt, []string{"layers"}); err == nil {
		t.Fatal("want an error when gh fails to resolve the stack")
	}
}

// forEachLayersSetup is the shape the feature exists for: one tab repeated
// once per layer, its name, cwd and prompt all reaching into the per-element
// fields resolveLists produces.
func forEachLayersSetup() setup.Setup {
	return setup.Setup{
		Name:      "stack-review",
		AppliesTo: []string{"github_pr"},
		Tabs: []setup.Tab{
			{
				ForEach: "layers",
				As:      "layer",
				Name:    "L{{.layer_layer}} #{{.layer_pr}}",
				Cwd:     "{{.layer_head_branch}}",
				Panes: []setup.Pane{
					{
						Label:  "claude",
						Agent:  "claude",
						Submit: true,
						Prompt: "Review #{{.layer_pr}} at {{.layer_head_sha}} (base #{{.layer_base_pr}})",
					},
				},
			},
		},
	}
}

// This is the test that proves the feature actually works: a for_each:
// layers setup, applied to a real github_pr target, resolves through
// PreviewSetup into one tab per layer with names, cwds and prompts all
// carrying that layer's own fields.
func TestPreviewSetupResolvesLayersEndToEnd(t *testing.T) {
	_, cfg := existingRepo(t)
	prs := &fakePRLookup{
		info:  gh.PRInfo{Branch: "b101", Title: "middle"},
		stack: sampleStack(),
	}
	deps := Deps{PRs: prs}

	plan, tgt, err := PreviewSetup(deps, cfg, "https://github.com/phin-tech/herdr-phin-util/pull/101", forEachLayersSetup())
	if err != nil {
		t.Fatalf("PreviewSetup: %v", err)
	}
	if tgt.Kind != target.KindGitHubPR || tgt.Number != 101 {
		t.Fatalf("target = %+v, want the parsed github_pr", tgt)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("plan.Steps = %d, want 3 (one pane per layer)", len(plan.Steps))
	}

	wantTabName := []string{"L1 #100", "L2 #101", "L3 #102"}
	wantPrompt := []string{
		"Review #100 at sha100 (base #)",
		"Review #101 at sha101 (base #100)",
		"Review #102 at sha102 (base #101)",
	}
	wantCwdSuffix := []string{"b100", "b101", "b102"}
	for i, step := range plan.Steps {
		if step.TabName != wantTabName[i] {
			t.Errorf("step %d tab name = %q, want %q", i, step.TabName, wantTabName[i])
		}
		if step.Prompt != wantPrompt[i] {
			t.Errorf("step %d prompt = %q, want %q", i, step.Prompt, wantPrompt[i])
		}
		if !strings.HasSuffix(step.Cwd, wantCwdSuffix[i]) {
			t.Errorf("step %d cwd = %q, want it to end with %q", i, step.Cwd, wantCwdSuffix[i])
		}
	}
}

// A for_each naming a list nobody produces still fails with tabIterations'
// existing error, naming what actually was available -- here, "layers",
// since the setup's own first tab asked for it and got it.
func TestPreviewSetupForEachUnknownListNamesWhatWasAvailable(t *testing.T) {
	_, cfg := existingRepo(t)
	prs := &fakePRLookup{
		info:  gh.PRInfo{Branch: "b101", Title: "middle"},
		stack: sampleStack(),
	}
	deps := Deps{PRs: prs}

	def := setup.Setup{
		Name: "x",
		Tabs: []setup.Tab{
			{ForEach: "layers", Panes: []setup.Pane{{Command: "true"}}},
			{ForEach: "something_else", Panes: []setup.Pane{{Command: "true"}}},
		},
	}
	_, _, err := PreviewSetup(deps, cfg, "https://github.com/phin-tech/herdr-phin-util/pull/101", def)
	if err == nil {
		t.Fatal("want an error for the tab naming a list nobody produces")
	}
	if !strings.Contains(err.Error(), "something_else") || !strings.Contains(err.Error(), "layers") {
		t.Errorf("err = %q, want it to name both the missing list and what was available", err.Error())
	}
}

// A setup whose only for_each names something no target has ever produced
// gets the plain "provides no lists" wording, unchanged by any of this --
// resolveLists never even calls gh, since "something_else" is never a name
// it recognises.
func TestPreviewSetupForEachEntirelyUnknownListStillFailsPlainly(t *testing.T) {
	_, cfg := existingRepo(t)
	prs := &fakePRLookup{info: gh.PRInfo{Branch: "b101"}}
	deps := Deps{PRs: prs}

	def := setup.Setup{
		Name: "x",
		Tabs: []setup.Tab{{ForEach: "something_else", Panes: []setup.Pane{{Command: "true"}}}},
	}
	_, _, err := PreviewSetup(deps, cfg, "https://github.com/phin-tech/herdr-phin-util/pull/101", def)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "provides no lists") {
		t.Errorf("err = %q, want the plain \"provides no lists\" wording", err.Error())
	}
	if prs.stackCalls != 0 {
		t.Errorf("Stack called %d times, want 0", prs.stackCalls)
	}
}

// applySetup is the real-run path's own wiring (PreviewSetup's is exercised
// above): it too must resolve "layers" and build one tab per element.
func TestApplySetupResolvesLayersFromAGitHubPRTarget(t *testing.T) {
	l := &fakeLayout{}
	prs := &fakePRLookup{stack: sampleStack()}
	tgt := target.Target{Kind: target.KindGitHubPR, Owner: "o", Repo: "r", Number: 101}

	plan, panes, problems, err := applySetup(
		Deps{Session: &fakeSession{}, Layout: l, PRs: prs}, &config.Settings{}, tgt,
		forEachLayersSetup(), rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none", problems)
	}
	if len(panes) != 3 {
		t.Fatalf("panes = %v, want one per layer", panes)
	}
	if prs.stackCalls != 1 {
		t.Errorf("Stack called %d times, want 1", prs.stackCalls)
	}
	if len(plan.Steps) != 3 || plan.Steps[1].TabName != "L2 #101" {
		t.Errorf("plan.Steps = %+v, want 3 steps with the second named L2 #101", plan.Steps)
	}
}

// If the setup asks for layers and gh fails, that is a resolve failure --
// an error, and nothing gets built. By the time applySetup runs, the Space
// and its worktree already exist (runAgentStep only calls this after both),
// so this failure comes after real state has already been created; that
// ordering already exists for every other resolve failure today and is not
// what this change is trying to fix, just what it inherits honestly.
func TestApplySetupForEachGhFailureIsAnErrorAndBuildsNothing(t *testing.T) {
	l := &fakeLayout{}
	prs := &fakePRLookup{stackErr: errors.New("gh: rate limited")}
	tgt := target.Target{Kind: target.KindGitHubPR, Owner: "o", Repo: "r", Number: 101}

	_, _, _, err := applySetup(
		Deps{Session: &fakeSession{}, Layout: l, PRs: prs}, &config.Settings{}, tgt,
		forEachLayersSetup(), rootPane(), "w1", "/repo", "/repo", nil)
	if err == nil {
		t.Fatal("want an error when gh fails to resolve the stack")
	}
	if len(l.calls) != 0 {
		t.Errorf("layout calls = %v, want none -- nothing should be built on a failed resolve", l.calls)
	}
}
