package session

import (
	"errors"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/gh"
	"github.com/phin-tech/herdr-phin-util/internal/setup"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// fakeAgentKindNamer is the sliver of config.Settings SetupRows' default row
// needs.
type fakeAgentKindNamer struct{ kind string }

func (f fakeAgentKindNamer) AgentKind() string { return f.kind }

// fakeStackLookup is open.PRLookup, recording whether Stacks (plural) was
// ever called -- the assertion the lazy-gh tests are actually about. LookupPR
// and LookupIssue are never used by SetupRows, so they are stubs that fail
// the test if that assumption ever stops being true.
type fakeStackLookup struct {
	paths [][]gh.StackPR
	err   error
	calls int
}

func (f *fakeStackLookup) LookupPR(owner, repo string, number int) (gh.PRInfo, error) {
	return gh.PRInfo{}, errors.New("SetupRows should never call LookupPR")
}

func (f *fakeStackLookup) LookupIssue(owner, repo string, number int) (gh.IssueInfo, error) {
	return gh.IssueInfo{}, errors.New("SetupRows should never call LookupIssue")
}

func (f *fakeStackLookup) Stack(owner, repo string, number int) ([]gh.StackPR, error) {
	return nil, errors.New("SetupRows should call Stacks (plural), not Stack")
}

func (f *fakeStackLookup) Stacks(owner, repo string, number int) ([][]gh.StackPR, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.paths, nil
}

// prCandidate is a KindLink row for a pull request, the only shape
// stackedSubject ever has anything to resolve for.
func prCandidate() Candidate {
	tgt := target.Parse("https://github.com/phin-tech/roux/pull/42")
	return Candidate{Kind: KindLink, Target: tgt}
}

func stackSetup() []setup.Setup {
	return []setup.Setup{{Name: "stack-review", AppliesTo: []string{"github_stack"}, Tabs: []setup.Tab{{Name: "a"}}}}
}

func plainPRSetup() []setup.Setup {
	return []setup.Setup{{Name: "pr-review", AppliesTo: []string{"github_pr"}, Tabs: []setup.Tab{{Name: "a"}}}}
}

// The lazy-gh contract, mirroring how internal/open/stack_test.go pins
// resolveLists' own laziness: no candidate setup mentions github_stack, so
// stackedSubject must not spend a network round trip finding out something
// nothing is going to ask about.
func TestSetupRowsDoesNotCallStacksWhenNoSetupMentionsGitHubStack(t *testing.T) {
	prs := &fakeStackLookup{paths: [][]gh.StackPR{{{Number: 1}, {Number: 2}}}}
	load := func(string) []setup.Setup { return plainPRSetup() }

	SetupRows(load, prs, fakeAgentKindNamer{"claude"}, prCandidate())

	if prs.calls != 0 {
		t.Errorf("Stacks called %d times, want 0 -- nothing asked applies_to: [github_stack]", prs.calls)
	}
}

// The other half: once a candidate setup does name github_stack, resolving
// stackness is exactly what has to happen for that setup to ever be able to
// match.
func TestSetupRowsCallsStacksWhenASetupMentionsGitHubStack(t *testing.T) {
	prs := &fakeStackLookup{paths: [][]gh.StackPR{{{Number: 1}, {Number: 2}}}}
	load := func(string) []setup.Setup { return stackSetup() }

	SetupRows(load, prs, fakeAgentKindNamer{"claude"}, prCandidate())

	if prs.calls != 1 {
		t.Errorf("Stacks called %d times, want 1", prs.calls)
	}
}

// A stacked PR's row offers the github_stack setup.
func TestSetupRowsOffersAGitHubStackSetupForAStackedPR(t *testing.T) {
	prs := &fakeStackLookup{paths: [][]gh.StackPR{{{Number: 1}, {Number: 2}, {Number: 3}}}}
	load := func(string) []setup.Setup { return stackSetup() }

	rows := SetupRows(load, prs, fakeAgentKindNamer{"claude"}, prCandidate())

	if !hasSetupRow(rows, "stack-review") {
		t.Errorf("rows = %+v, want stack-review offered for a stacked PR", rows)
	}
}

// A lone (one-layer) PR does not get the github_stack setup offered -- Q3's
// answer, exercised through the row-building path rather than Matches
// directly.
func TestSetupRowsDoesNotOfferAGitHubStackSetupForALonePR(t *testing.T) {
	prs := &fakeStackLookup{paths: [][]gh.StackPR{{{Number: 42}}}}
	load := func(string) []setup.Setup { return stackSetup() }

	rows := SetupRows(load, prs, fakeAgentKindNamer{"claude"}, prCandidate())

	if hasSetupRow(rows, "stack-review") {
		t.Errorf("rows = %+v, want stack-review NOT offered for a one-layer chain", rows)
	}
}

// A fork -- more than one path to a tip -- still counts as stacked as long
// as any path has 2+ layers (Q4), and must not make SetupRows blow up: a
// picker row cannot afford to fail open just because Stack itself would
// refuse to pick one chain.
func TestSetupRowsTreatsAForkAsStacked(t *testing.T) {
	prs := &fakeStackLookup{paths: [][]gh.StackPR{
		{{Number: 1}, {Number: 2}},
		{{Number: 1}, {Number: 3}},
	}}
	load := func(string) []setup.Setup { return stackSetup() }

	rows := SetupRows(load, prs, fakeAgentKindNamer{"claude"}, prCandidate())

	if !hasSetupRow(rows, "stack-review") {
		t.Errorf("rows = %+v, want stack-review offered for a fork with a 2+ layer path", rows)
	}
}

// A gh failure degrades to "not stacked" -- a network blip must not stop the
// setup level from opening at all, and the default row plus any plain
// github_pr setup still has to come back.
func TestSetupRowsDegradesToNotStackedOnGhFailure(t *testing.T) {
	prs := &fakeStackLookup{err: errors.New("gh: rate limited")}
	load := func(string) []setup.Setup { return append(stackSetup(), plainPRSetup()...) }

	rows := SetupRows(load, prs, fakeAgentKindNamer{"claude"}, prCandidate())

	if hasSetupRow(rows, "stack-review") {
		t.Errorf("rows = %+v, want stack-review NOT offered once gh failed", rows)
	}
	if !hasSetupRow(rows, "pr-review") {
		t.Errorf("rows = %+v, want pr-review still offered", rows)
	}
	if len(rows) == 0 || rows[0].Label != DefaultSetupLabel {
		t.Errorf("rows = %+v, want the default row even after a gh failure", rows)
	}
}

func hasSetupRow(rows []Candidate, name string) bool {
	for _, r := range rows {
		if r.Label == name {
			return true
		}
	}
	return false
}
