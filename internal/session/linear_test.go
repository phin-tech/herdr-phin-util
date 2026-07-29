package session

import (
	"strings"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/target"
)

func linearTicket(t *testing.T) target.Target {
	t.Helper()
	tgt := target.Parse("https://linear.app/phin/issue/ENG-123/fix-the-thing")
	if tgt.Kind != target.KindLinear {
		t.Fatalf("kind = %q, want linear", tgt.Kind)
	}
	return tgt
}

func repoWithBranches() (RepoContext, []Candidate) {
	repo := RepoContext{Root: "/src/app", Name: "app", DefaultBranch: "main"}
	return repo, []Candidate{
		{Kind: KindWorktree, Label: "main", Branch: "main"},
		{Kind: KindBranch, Label: "release-0.5", Branch: "release-0.5"},
		{Kind: KindRemoteBranch, Label: "feature-x", Branch: "feature-x"},
		{Kind: KindPrunable, Label: "gone", Branch: "gone"},
	}
}

// The default branch leads, and it leads as origin's copy.
func TestLinearBaseRowsLeadWithTheDefaultBranch(t *testing.T) {
	repo, branches := repoWithBranches()

	rows := LinearBaseRows(repo, linearTicket(t), branches)

	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	if got, want := rows[0].Base, "origin/main"; got != want {
		t.Errorf("first base = %q, want %q", got, want)
	}
	if got, want := rows[0].Detail, "default branch"; got != want {
		t.Errorf("detail = %q, want %q", got, want)
	}
}

// Every row cuts the same branch -- the one the ticket named. Only the base
// differs, which is the whole reason these are rows rather than a prompt.
func TestLinearBaseRowsAllCutTheTicketsBranch(t *testing.T) {
	repo, branches := repoWithBranches()
	tgt := linearTicket(t)

	rows := LinearBaseRows(repo, tgt, branches)

	for _, r := range rows {
		if r.Branch != "eng-123-fix-the-thing" {
			t.Errorf("row %q cuts %q, want the ticket's branch", r.Label, r.Branch)
		}
		if r.Kind != KindLinearBase {
			t.Errorf("row %q kind = %q", r.Label, r.Kind)
		}
		if r.Target.Issue != tgt.Issue {
			t.Errorf("row %q lost the ticket", r.Label)
		}
	}
}

// Two rows for the same ref would be a choice with no difference in it.
// origin/main and a local main are not that: the remote's copy can be ahead,
// which is the entire reason the default row prefers it.
func TestLinearBaseRowsDoNotRepeatABase(t *testing.T) {
	repo, branches := repoWithBranches()

	rows := LinearBaseRows(repo, linearTicket(t), branches)

	seen := map[string]int{}
	for _, r := range rows {
		seen[r.Base]++
	}
	for base, n := range seen {
		if n > 1 {
			t.Errorf("base %q appears %d times", base, n)
		}
	}
	if seen["origin/main"] != 1 || seen["main"] != 1 {
		t.Errorf("want one row each for origin/main and main, got %d and %d",
			seen["origin/main"], seen["main"])
	}
}

// A worktree that git can no longer find is not somewhere to start a branch.
func TestLinearBaseRowsSkipPrunableWorktrees(t *testing.T) {
	repo, branches := repoWithBranches()

	for _, r := range LinearBaseRows(repo, linearTicket(t), branches) {
		if strings.Contains(r.Base, "gone") {
			t.Errorf("prunable worktree offered as a base: %q", r.Label)
		}
	}
}

func TestLinearBaseRowsQualifyRemoteBranches(t *testing.T) {
	repo, branches := repoWithBranches()

	var found bool
	for _, r := range LinearBaseRows(repo, linearTicket(t), branches) {
		if r.Base == "origin/feature-x" {
			found = true
			if r.Detail != "remote branch" {
				t.Errorf("detail = %q", r.Detail)
			}
		}
		if r.Base == "feature-x" {
			t.Error("a remote-only branch was offered unqualified")
		}
	}
	if !found {
		t.Error("no row for the remote branch")
	}
}

// A ticket URL with no slug still names a branch, so it still has bases.
func TestLinearBaseRowsWorkWithoutASlug(t *testing.T) {
	repo, branches := repoWithBranches()
	tgt := target.Parse("https://linear.app/phin/issue/ENG-9")

	rows := LinearBaseRows(repo, tgt, branches)

	if len(rows) == 0 {
		t.Fatal("no rows for a slugless ticket")
	}
	if rows[0].Branch != "eng-9" {
		t.Errorf("branch = %q, want eng-9", rows[0].Branch)
	}
}

// origin/main is the better starting point and its absence is not a failure,
// which is the same trade KindNewBranch already makes.
func TestLinearBaseFallbackStripsTheRemote(t *testing.T) {
	if got, want := LinearBaseFallback("origin/main"), "main"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := LinearBaseFallback("main"); got != "" {
		t.Errorf("a local base needs no fallback, got %q", got)
	}
}

// The ticket's kind is what makes `applies_to: [linear]` match, and the
// checkout's name is what makes `repos:` match. Neither is knowable alone.
func TestSetupSubjectForALinearBaseKnowsBothHalves(t *testing.T) {
	tgt := linearTicket(t)
	sub := SetupSubject(Candidate{
		Kind:   KindLinearBase,
		Path:   "/src/github.com/phin-tech/app",
		Branch: "eng-123-fix-the-thing",
		Target: tgt,
	})

	if sub.Kind != target.KindLinear {
		t.Errorf("kind = %q, want linear", sub.Kind)
	}
	if sub.Repo != "app" || sub.RepoName != "app" {
		t.Errorf("repo = %q / %q, want app", sub.Repo, sub.RepoName)
	}
	if sub.Branch != "eng-123-fix-the-thing" {
		t.Errorf("branch = %q", sub.Branch)
	}
}
