// Package target works out what a pasted string refers to.
//
// Everything here is pure parsing: no network, no filesystem. What a target
// means locally -- which checkout, which branch exists -- is decided later, so
// that this layer stays cheap to test against real-world URLs.
package target

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Kind is the sort of thing a pasted string turned out to be.
type Kind string

const (
	// KindGitHubPR is a pull request URL.
	KindGitHubPR Kind = "github_pr"
	// KindGitHubIssue is an issue URL. Unlike a pull request it names no
	// existing branch -- there is nothing to check out yet, so it behaves like
	// a Linear issue: derive a branch and create it.
	KindGitHubIssue Kind = "github_issue"
	// KindGitHubRepo is a repository reference with no issue or pull request
	// attached: a clone URL, or "owner/repo" shorthand. It names a checkout
	// rather than a unit of work, so what happens next depends entirely on
	// whether that checkout exists on this machine yet.
	KindGitHubRepo Kind = "github_repo"
	// KindGitHubStack refines KindGitHubPR rather than rivalling it: Parse
	// never returns this value, and never will. Whether a pull request is
	// stacked (its baseRefName chain has 2+ layers) is not something a URL
	// says -- it takes a gh call to find out -- so it cannot be a parse-time
	// decision the way the kinds above are. It exists purely as vocabulary
	// for a setup's applies_to: [github_stack], matched by
	// [setup.Setup.Matches] against [setup.Subject.Stacked], which is
	// resolved lazily well after Parse has already said KindGitHubPR. See
	// that field's doc comment for the full story.
	KindGitHubStack Kind = "github_stack"
	// KindLinear is a Linear issue URL.
	KindLinear Kind = "linear"
	// KindPlain is anything else: a name for a Space, not a reference.
	KindPlain Kind = "plain"
	// KindProject is a local checkout picked from disk. Parse never returns
	// it -- it is constructed by the picker, which knows it is holding a
	// directory rather than something someone typed -- but it is a Kind so it
	// gets its own prompt template like every other sort of Space.
	KindProject Kind = "project"
)

// Target is a parsed reference. Fields not relevant to Kind are empty.
type Target struct {
	Kind Kind
	// URL is the input when it was a URL, else empty.
	URL string
	// Text is the raw input, always set.
	Text string

	// Host, Owner and Repo identify a GitHub repository.
	Host  string
	Owner string
	Repo  string
	// Number is the pull request number.
	Number int

	// Issue is a Linear issue key such as "ENG-123".
	Issue string
	// Slug is the title fragment Linear puts in its URLs, already
	// hyphen-separated: "fix-the-thing".
	Slug string
}

// prPath matches the tail of a pull request URL: /owner/repo/pull/123, with
// anything after it (/files, #discussion) ignored.
var prPath = regexp.MustCompile(`^/([^/]+)/([^/]+)/pull/(\d+)`)

// issuePath is the same shape for issues. GitHub numbers issues and pull
// requests from one sequence per repository, so "repo#7" identifies exactly
// one of them and the two kinds can never collide on a label.
var issuePath = regexp.MustCompile(`^/([^/]+)/([^/]+)/issues/(\d+)`)

// repoPath matches a bare repository URL: /owner/repo, with an optional .git
// suffix and trailing slash. Exactly two segments, so it cannot swallow the
// /pull/ and /issues/ forms above.
var repoPath = regexp.MustCompile(`^/([^/]+)/([^/]+?)(?:\.git)?/?$`)

// sshRepo matches the scp-style remote git prints for a private clone.
var sshRepo = regexp.MustCompile(`^git@github\.com:([^/]+)/([^/]+?)(?:\.git)?/?$`)

// shorthandRepo matches "owner/repo" typed by hand. It is deliberately strict
// about the character set: a branch name like "codex/iterm-split" would
// otherwise read as a repository, and the two are told apart by nothing else.
var shorthandRepo = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]*)/([A-Za-z0-9][A-Za-z0-9._-]*)$`)

// linearPath matches /<org>/issue/<KEY>/<slug>, where the slug is optional
// because Linear's "copy link" sometimes omits it.
var linearPath = regexp.MustCompile(`^/[^/]+/issue/([A-Za-z][A-Za-z0-9]*-\d+)(?:/([^/?#]*))?`)

// Parse classifies a pasted string.
//
// Anything that is not a URL this package recognises is KindPlain rather than
// an error: a bare string is a legitimate way to name a new Space, so there is
// no such thing as unparseable input.
func Parse(input string) Target {
	text := strings.TrimSpace(input)
	t := Target{Kind: KindPlain, Text: text}
	if text == "" {
		return t
	}

	if m := sshRepo.FindStringSubmatch(text); m != nil {
		return repoTarget(text, m[1], m[2])
	}

	u, err := url.Parse(text)
	if err != nil || u.Host == "" {
		return t
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")

	switch {
	case host == "github.com":
		if m := prPath.FindStringSubmatch(u.Path); m != nil {
			// The regexp already constrained this to digits.
			number, _ := strconv.Atoi(m[3])
			return Target{
				Kind:   KindGitHubPR,
				URL:    text,
				Text:   text,
				Host:   "github.com",
				Owner:  m[1],
				Repo:   m[2],
				Number: number,
			}
		}
		if m := issuePath.FindStringSubmatch(u.Path); m != nil {
			number, _ := strconv.Atoi(m[3])
			return Target{
				Kind:   KindGitHubIssue,
				URL:    text,
				Text:   text,
				Host:   "github.com",
				Owner:  m[1],
				Repo:   m[2],
				Number: number,
			}
		}
		if m := repoPath.FindStringSubmatch(u.Path); m != nil {
			return repoTarget(text, m[1], m[2])
		}
		return t

	case host == "linear.app":
		m := linearPath.FindStringSubmatch(u.Path)
		if m == nil {
			return t
		}
		return Target{
			Kind:  KindLinear,
			URL:   text,
			Text:  text,
			Issue: strings.ToUpper(m[1]),
			Slug:  m[2],
		}
	}
	return t
}

// ParseRepoShorthand recognises "owner/repo" typed without a URL around it.
//
// It is separate from Parse on purpose. A bare string with a slash in it is a
// perfectly good Space name, and at the worktree level it is far more likely
// to be a branch, so only a caller that knows it is looking at a repository
// list should ask this question.
func ParseRepoShorthand(input string) (Target, bool) {
	text := strings.TrimSpace(input)
	m := shorthandRepo.FindStringSubmatch(text)
	if m == nil {
		return Target{}, false
	}
	return repoTarget(text, m[1], m[2]), true
}

func repoTarget(text, owner, repo string) Target {
	return Target{
		Kind:  KindGitHubRepo,
		URL:   text,
		Text:  text,
		Host:  "github.com",
		Owner: owner,
		Repo:  repo,
	}
}

// CloneURL is the address to clone a repository target from.
func (t Target) CloneURL() string {
	if t.Owner == "" || t.Repo == "" {
		return ""
	}
	host := t.Host
	if host == "" {
		host = "github.com"
	}
	return fmt.Sprintf("https://%s/%s/%s.git", host, t.Owner, t.Repo)
}

// Branch is the branch name to create for a target.
//
// Only Linear gets a generated branch: a pull request already has one, and it
// must be matched exactly rather than guessed.
func (t Target) Branch() string {
	switch t.Kind {
	case KindLinear:
		key := strings.ToLower(t.Issue)
		if t.Slug == "" {
			return key
		}
		return key + "-" + Sanitize(t.Slug)
	case KindGitHubIssue:
		// An issue URL carries no title the way a Linear one does, so this is
		// the most a URL alone can say. Slug is filled in by whoever asks gh
		// for the title, and is preferred when it is there.
		if t.Slug != "" {
			return fmt.Sprintf("%d-%s", t.Number, Sanitize(t.Slug))
		}
		return fmt.Sprintf("issue-%d", t.Number)
	default:
		return ""
	}
}

// Label is the Space name for a target.
func (t Target) Label() string {
	switch t.Kind {
	case KindGitHubPR, KindGitHubIssue:
		return fmt.Sprintf("%s#%d", t.Repo, t.Number)
	case KindLinear:
		return t.Issue
	case KindGitHubRepo:
		return t.Repo
	default:
		return t.Text
	}
}

// sanitizeStrip removes characters git refuses in a ref, plus the shell-hostile
// ones that make a path awkward to type.
var sanitizeStrip = regexp.MustCompile(`[^a-zA-Z0-9._/-]+`)

// Sanitize turns arbitrary text into something usable as a git branch name.
func Sanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = sanitizeStrip.ReplaceAllString(s, "-")
	// Git rejects leading/trailing dots and slashes, doubled dots, and a
	// trailing ".lock"; trimming the separators covers the cases a title
	// realistically produces.
	s = strings.Trim(s, "-./")
	s = strings.ReplaceAll(s, "..", "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return s
}
