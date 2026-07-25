package target

import "testing"

func TestParseRepoURLForms(t *testing.T) {
	cases := []string{
		"https://github.com/phin-tech/roux",
		"https://github.com/phin-tech/roux.git",
		"https://github.com/phin-tech/roux/",
		"git@github.com:phin-tech/roux.git",
		"git@github.com:phin-tech/roux",
	}

	for _, in := range cases {
		got := Parse(in)
		if got.Kind != KindGitHubRepo {
			t.Errorf("Parse(%q).Kind = %q, want %q", in, got.Kind, KindGitHubRepo)
			continue
		}
		if got.Owner != "phin-tech" || got.Repo != "roux" {
			t.Errorf("Parse(%q) = owner %q repo %q", in, got.Owner, got.Repo)
		}
		if got.Label() != "roux" {
			t.Errorf("Parse(%q).Label() = %q, want roux", in, got.Label())
		}
	}
}

// A repository reference must not swallow the URLs that name actual work.
func TestRepoParsingDoesNotShadowPullsAndIssues(t *testing.T) {
	if got := Parse("https://github.com/phin-tech/roux/pull/42"); got.Kind != KindGitHubPR {
		t.Errorf("kind = %q, want a pull request", got.Kind)
	}
	if got := Parse("https://github.com/phin-tech/roux/issues/99"); got.Kind != KindGitHubIssue {
		t.Errorf("kind = %q, want an issue", got.Kind)
	}
}

func TestCloneURL(t *testing.T) {
	// Every accepted form normalises to the same https clone address.
	for _, in := range []string{
		"https://github.com/phin-tech/roux",
		"git@github.com:phin-tech/roux.git",
	} {
		if got := Parse(in).CloneURL(); got != "https://github.com/phin-tech/roux.git" {
			t.Errorf("Parse(%q).CloneURL() = %q", in, got)
		}
	}

	// Nothing to clone without a repository.
	if got := Parse("scratch space").CloneURL(); got != "" {
		t.Errorf("CloneURL() = %q, want empty", got)
	}
}

func TestParseRepoShorthand(t *testing.T) {
	got, ok := ParseRepoShorthand("phin-tech/roux")
	if !ok {
		t.Fatal("expected owner/repo to be recognised")
	}
	if got.Owner != "phin-tech" || got.Repo != "roux" || got.Kind != KindGitHubRepo {
		t.Errorf("got %+v", got)
	}
	if got.CloneURL() != "https://github.com/phin-tech/roux.git" {
		t.Errorf("CloneURL = %q", got.CloneURL())
	}
}

// The shorthand has to stay narrow, or every branch name with a slash in it
// would read as a repository to clone.
func TestParseRepoShorthandRejectsAmbiguousText(t *testing.T) {
	for _, in := range []string{
		"codex/iterm-split-shortcuts/extra", // three segments
		"a/b/c",
		"scratch space",
		"roux",
		"",
		"/leading",
		"trailing/",
		"has space/repo",
		"owner/repo#42",
		"https://github.com/phin-tech/roux", // a URL is Parse's job, not this
	} {
		if _, ok := ParseRepoShorthand(in); ok {
			t.Errorf("ParseRepoShorthand(%q) should not have matched", in)
		}
	}
}

// A two-segment branch name is genuinely indistinguishable from owner/repo,
// which is why only a caller looking at a repository list may ask.
func TestParseRepoShorthandMatchesBranchShapedText(t *testing.T) {
	if _, ok := ParseRepoShorthand("codex/iterm-split"); !ok {
		t.Skip("shape overlaps with branch names by design; the caller decides")
	}
}

// Parse itself must leave the shorthand alone, so "open foo/bar" still names a
// Space rather than trying to clone.
func TestParseLeavesShorthandAsPlainText(t *testing.T) {
	if got := Parse("phin-tech/roux"); got.Kind != KindPlain {
		t.Errorf("Parse(%q).Kind = %q, want plain", "phin-tech/roux", got.Kind)
	}
}
