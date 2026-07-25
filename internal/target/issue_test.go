package target

import "testing"

func TestGitHubIssueBranch(t *testing.T) {
	// From a URL alone there is no title, so the number is all there is.
	bare := Parse("https://github.com/phin-tech/roux/issues/99")
	if got := bare.Branch(); got != "issue-99" {
		t.Errorf("Branch() = %q, want issue-99", got)
	}

	// Once something has asked gh for the title, the slug is preferred.
	enriched := bare
	enriched.Slug = "Fix the flaky test!"
	if got, want := enriched.Branch(), "99-fix-the-flaky-test"; got != want {
		t.Errorf("Branch() = %q, want %q", got, want)
	}
}

// GitHub numbers issues and pull requests from one sequence per repository, so
// a "repo#N" label is unambiguous even though two kinds produce it.
func TestGitHubIssueAndPRShareALabelSpace(t *testing.T) {
	pr := Parse("https://github.com/phin-tech/roux/pull/7")
	issue := Parse("https://github.com/phin-tech/roux/issues/8")

	if pr.Label() != "roux#7" {
		t.Errorf("PR label = %q", pr.Label())
	}
	if issue.Label() != "roux#8" {
		t.Errorf("issue label = %q", issue.Label())
	}
}

func TestGitHubIssueIgnoresTrailingPath(t *testing.T) {
	got := Parse("https://github.com/phin-tech/roux/issues/99#issuecomment-123")
	if got.Kind != KindGitHubIssue || got.Number != 99 {
		t.Errorf("got kind %q number %d", got.Kind, got.Number)
	}
}

// A pull request still wins, and still reports no branch of its own -- the
// real one has to come from gh rather than be guessed.
func TestPullRequestStillHasNoDerivedBranch(t *testing.T) {
	if got := Parse("https://github.com/phin-tech/roux/pull/7").Branch(); got != "" {
		t.Errorf("Branch() = %q, want empty for a pull request", got)
	}
}
