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

	u, err := url.Parse(text)
	if err != nil || u.Host == "" {
		return t
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")

	switch {
	case host == "github.com":
		m := prPath.FindStringSubmatch(u.Path)
		if m == nil {
			return t
		}
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
	default:
		return ""
	}
}

// Label is the Space name for a target.
func (t Target) Label() string {
	switch t.Kind {
	case KindGitHubPR:
		return fmt.Sprintf("%s#%d", t.Repo, t.Number)
	case KindLinear:
		return t.Issue
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
