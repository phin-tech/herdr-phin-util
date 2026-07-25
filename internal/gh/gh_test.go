package gh

import (
	"errors"
	"strings"
	"testing"
)

func fakeRunner(out string, err error, calls *[][]string) CommandRunner {
	return func(dir, name string, args ...string) ([]byte, error) {
		call := append([]string{name}, args...)
		*calls = append(*calls, call)
		if err != nil {
			return nil, err
		}
		return []byte(out), nil
	}
}

func TestLookupPRParsesBranchAndTitle(t *testing.T) {
	var calls [][]string
	c := &Client{run: fakeRunner(`{"headRefName":"fix-thing","title":"Fix the thing"}`, nil, &calls)}

	got, err := c.LookupPR("phin-tech", "herdr-phin-util", 42)
	if err != nil {
		t.Fatalf("LookupPR: %v", err)
	}
	if got.Branch != "fix-thing" || got.Title != "Fix the thing" {
		t.Errorf("LookupPR = %+v", got)
	}

	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	got1 := strings.Join(calls[0], " ")
	want := "gh pr view 42 --repo phin-tech/herdr-phin-util --json headRefName,title"
	if got1 != want {
		t.Errorf("call = %q, want %q", got1, want)
	}
}

// A private repo without access, or gh not being logged in, must surface as a
// wrapped error rather than a confusing JSON decode failure on empty output.
func TestLookupPRPropagatesCommandError(t *testing.T) {
	var calls [][]string
	c := &Client{run: fakeRunner("", errors.New("exit status 1"), &calls)}

	if _, err := c.LookupPR("o", "r", 1); err == nil {
		t.Fatal("want an error when the gh command fails")
	}
}

func TestLookupPRRejectsUnparsableOutput(t *testing.T) {
	var calls [][]string
	c := &Client{run: fakeRunner("not json", nil, &calls)}

	if _, err := c.LookupPR("o", "r", 1); err == nil {
		t.Fatal("want an error when gh's output is not the JSON we expect")
	}
}
