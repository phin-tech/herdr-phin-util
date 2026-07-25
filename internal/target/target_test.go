package target

import "testing"

func TestParseGitHubPR(t *testing.T) {
	cases := []string{
		"https://github.com/phin-tech/herdr-phin-util/pull/42",
		// The tail varies with where the link was copied from.
		"https://github.com/phin-tech/herdr-phin-util/pull/42/files",
		"https://github.com/phin-tech/herdr-phin-util/pull/42#issuecomment-1",
		"https://www.github.com/phin-tech/herdr-phin-util/pull/42",
	}
	for _, in := range cases {
		got := Parse(in)
		if got.Kind != KindGitHubPR {
			t.Errorf("Parse(%q).Kind = %q, want github_pr", in, got.Kind)
			continue
		}
		if got.Owner != "phin-tech" || got.Repo != "herdr-phin-util" || got.Number != 42 {
			t.Errorf("Parse(%q) = owner %q repo %q number %d", in, got.Owner, got.Repo, got.Number)
		}
	}
}

// An issue URL is not a pull request; treating it as one would send us looking
// for a branch that does not exist.
func TestParseGitHubNonPRIsPlain(t *testing.T) {
	for _, in := range []string{
		"https://github.com/phin-tech/herdr-phin-util/issues/42",
		"https://github.com/phin-tech/herdr-phin-util",
	} {
		if got := Parse(in); got.Kind != KindPlain {
			t.Errorf("Parse(%q).Kind = %q, want plain", in, got.Kind)
		}
	}
}

func TestParseLinear(t *testing.T) {
	got := Parse("https://linear.app/phin/issue/ENG-123/fix-the-flaky-test")
	if got.Kind != KindLinear {
		t.Fatalf("Kind = %q, want linear", got.Kind)
	}
	if got.Issue != "ENG-123" {
		t.Errorf("Issue = %q, want ENG-123", got.Issue)
	}
	if got.Slug != "fix-the-flaky-test" {
		t.Errorf("Slug = %q", got.Slug)
	}
	if want := "eng-123-fix-the-flaky-test"; got.Branch() != want {
		t.Errorf("Branch() = %q, want %q", got.Branch(), want)
	}
}

// Linear's "copy link" sometimes stops at the issue key.
func TestParseLinearWithoutSlug(t *testing.T) {
	got := Parse("https://linear.app/phin/issue/ENG-123")
	if got.Kind != KindLinear || got.Issue != "ENG-123" {
		t.Fatalf("Kind = %q, Issue = %q", got.Kind, got.Issue)
	}
	if got.Branch() != "eng-123" {
		t.Errorf("Branch() = %q, want eng-123", got.Branch())
	}
}

func TestParseLowercasesIssueKeyToUpper(t *testing.T) {
	if got := Parse("https://linear.app/phin/issue/eng-7/x"); got.Issue != "ENG-7" {
		t.Errorf("Issue = %q, want ENG-7", got.Issue)
	}
}

func TestParsePlain(t *testing.T) {
	for _, in := range []string{"scratch pad", "", "  spaced  ", "not a url"} {
		if got := Parse(in); got.Kind != KindPlain {
			t.Errorf("Parse(%q).Kind = %q, want plain", in, got.Kind)
		}
	}
	if got := Parse("  spaced  "); got.Text != "spaced" {
		t.Errorf("Text = %q, want the trimmed input", got.Text)
	}
}

// A pull request's branch is whatever GitHub says it is, so guessing one would
// be actively wrong.
func TestBranchOnlyGeneratedForLinear(t *testing.T) {
	pr := Parse("https://github.com/o/r/pull/1")
	if pr.Branch() != "" {
		t.Errorf("PR Branch() = %q, want empty", pr.Branch())
	}
	if Parse("just a name").Branch() != "" {
		t.Error("plain Branch() should be empty")
	}
}

func TestLabel(t *testing.T) {
	if got := Parse("https://github.com/o/herdr/pull/9").Label(); got != "herdr#9" {
		t.Errorf("Label() = %q, want herdr#9", got)
	}
	if got := Parse("https://linear.app/p/issue/ENG-3/x").Label(); got != "ENG-3" {
		t.Errorf("Label() = %q, want ENG-3", got)
	}
	if got := Parse("my space").Label(); got != "my space" {
		t.Errorf("Label() = %q, want the raw text", got)
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"Fix the thing":        "fix-the-thing",
		"feat: add  support!":  "feat-add-support",
		"--leading-and-":       "leading-and",
		"dots..and..more":      "dots-and-more",
		"already-clean":        "already-clean",
		"UPPER/Case":           "upper/case",
		"trailing.lock":        "trailing.lock",
		"  spaces  everywhere": "spaces-everywhere",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
