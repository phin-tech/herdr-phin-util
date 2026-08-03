package gh

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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

// prFixture is one row of a canned `gh pr list` response.
type prFixture struct {
	Number int
	Title  string
	Head   string
	Base   string
	SHA    string
}

// stackFixture renders a set of open pull requests as the JSON gh pr list
// would emit, in the order given -- deliberately not sorted, since the walk
// must not depend on gh's own ordering.
func stackFixture(prs []prFixture) string {
	var b strings.Builder
	b.WriteString("[")
	for i, pr := range prs {
		if i > 0 {
			b.WriteString(",")
		}
		url := fmt.Sprintf("https://github.com/o/r/pull/%d", pr.Number)
		sha := pr.SHA
		if sha == "" {
			sha = fmt.Sprintf("sha%d", pr.Number)
		}
		fmt.Fprintf(&b, `{"number":%d,"title":%q,"url":%q,"headRefName":%q,"baseRefName":%q,"headRefOid":%q}`,
			pr.Number, pr.Title, url, pr.Head, pr.Base, sha)
	}
	b.WriteString("]")
	return b.String()
}

// A three-layer stack: #100 is bottom (based on trunk "main"), #101 is
// built on #100, #102 on #101. The bottom, middle and top must each
// reconstruct the identical chain, bottom-first.
func threeLayerStack() []prFixture {
	return []prFixture{
		{Number: 101, Title: "middle", Head: "b101", Base: "b100"},
		{Number: 100, Title: "bottom", Head: "b100", Base: "main"},
		{Number: 102, Title: "top", Head: "b102", Base: "b101"},
	}
}

func assertStackOrder(t *testing.T, got []StackPR, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("chain length = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i, n := range want {
		if got[i].Number != n {
			t.Errorf("chain[%d] = #%d, want #%d (full chain %+v)", i, got[i].Number, n, got)
		}
	}
}

func TestStackLinearThreeLayerFromBottom(t *testing.T) {
	var calls [][]string
	c := &Client{run: fakeRunner(stackFixture(threeLayerStack()), nil, &calls)}
	got, err := c.Stack("o", "r", 100)
	if err != nil {
		t.Fatalf("Stack: %v", err)
	}
	assertStackOrder(t, got, []int{100, 101, 102})
}

func TestStackLinearThreeLayerFromMiddle(t *testing.T) {
	var calls [][]string
	c := &Client{run: fakeRunner(stackFixture(threeLayerStack()), nil, &calls)}
	got, err := c.Stack("o", "r", 101)
	if err != nil {
		t.Fatalf("Stack: %v", err)
	}
	assertStackOrder(t, got, []int{100, 101, 102})
}

func TestStackLinearThreeLayerFromTop(t *testing.T) {
	var calls [][]string
	c := &Client{run: fakeRunner(stackFixture(threeLayerStack()), nil, &calls)}
	got, err := c.Stack("o", "r", 102)
	if err != nil {
		t.Fatalf("Stack: %v", err)
	}
	assertStackOrder(t, got, []int{100, 101, 102})

	// Field mapping, checked once here rather than in every test: the
	// bottom layer's base_pr-equivalent (BaseBranch) is the trunk, and the
	// fields line up with what gh reported for each PR.
	if got[2].Title != "top" || got[2].HeadBranch != "b102" || got[2].BaseBranch != "b101" {
		t.Errorf("top layer = %+v", got[2])
	}
	if got[0].BaseBranch != "main" {
		t.Errorf("bottom layer's base = %q, want main", got[0].BaseBranch)
	}
}

// A pull request that bases on the trunk directly and has nothing built on
// top of it belongs to no stack. That is a correct one-element chain, not an
// error -- most pull requests reviewed day to day are exactly this.
func TestStackStandalonePRYieldsOneElement(t *testing.T) {
	c := &Client{run: fakeRunner(stackFixture([]prFixture{
		{Number: 200, Title: "standalone", Head: "b200", Base: "main"},
		{Number: 201, Title: "unrelated", Head: "b201", Base: "main"},
	}), nil, new([][]string))}

	got, err := c.Stack("o", "r", 200)
	if err != nil {
		t.Fatalf("Stack: %v", err)
	}
	assertStackOrder(t, got, []int{200})
}

// A cycle (A based on B, B based on A -- malformed, but not impossible to
// construct by hand) must not hang the walk. The visited-set guard breaks
// out of it and returns some terminated, sane answer rather than looping
// forever.
func TestStackCycleDoesNotHang(t *testing.T) {
	c := &Client{run: fakeRunner(stackFixture([]prFixture{
		{Number: 1, Title: "one", Head: "b1", Base: "b2"},
		{Number: 2, Title: "two", Head: "b2", Base: "b1"},
	}), nil, new([][]string))}

	done := make(chan struct{})
	var got []StackPR
	var err error
	go func() {
		got, err = c.Stack("o", "r", 1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stack did not return -- looped on the cycle")
	}
	if err != nil {
		t.Fatalf("Stack: %v", err)
	}
	if len(got) == 0 || len(got) > 2 {
		t.Errorf("cycle chain = %+v, want 1 or 2 elements", got)
	}
}

// Two open pull requests sharing a base is a fork: the set of open PRs is a
// tree at that point, not a chain, and Stack must refuse to guess which
// branch continues "the" stack rather than picking one silently.
func TestStackForkIsRefusedNamingBothPaths(t *testing.T) {
	c := &Client{run: fakeRunner(stackFixture([]prFixture{
		{Number: 10, Title: "bottom", Head: "b10", Base: "main"},
		{Number: 11, Title: "left", Head: "b11", Base: "b10"},
		{Number: 12, Title: "right", Head: "b12", Base: "b10"},
	}), nil, new([][]string))}

	_, err := c.Stack("o", "r", 10)
	if err == nil {
		t.Fatal("want an error when two open PRs share a base")
	}
	if !strings.Contains(err.Error(), "#11") || !strings.Contains(err.Error(), "#12") {
		t.Errorf("fork error = %q, want it to name both #11 and #12", err.Error())
	}
}

// Asking about a PR *above* a fork is not ambiguous -- "what is this based
// on" has one answer however many siblings share that parent -- so it must
// succeed where TestStackForkIsRefusedNamingBothPaths refuses. Naming a tip
// is exactly how a person says which branch of the tree they meant, so this
// is the escape hatch that error points at.
func TestStackFromAboveAForkResolvesToThatPath(t *testing.T) {
	c := &Client{run: fakeRunner(stackFixture([]prFixture{
		{Number: 10, Title: "bottom", Head: "b10", Base: "main"},
		{Number: 11, Title: "left", Head: "b11", Base: "b10"},
		{Number: 12, Title: "right", Head: "b12", Base: "b10"},
	}), nil, new([][]string))}

	chain, err := c.Stack("o", "r", 12)
	if err != nil {
		t.Fatalf("asking about a tip above a fork should not be ambiguous: %v", err)
	}
	want := []int{10, 12}
	if len(chain) != len(want) {
		t.Fatalf("chain = %d layers, want %d", len(chain), len(want))
	}
	for i, n := range want {
		if chain[i].Number != n {
			t.Errorf("layer %d = #%d, want #%d", i+1, chain[i].Number, n)
		}
	}
}

func TestStackMissingTargetNumber(t *testing.T) {
	c := &Client{run: fakeRunner(stackFixture(threeLayerStack()), nil, new([][]string))}

	if _, err := c.Stack("o", "r", 999); err == nil {
		t.Fatal("want an error when the requested number is not among the open pull requests")
	}
}

func TestStackNoOpenPullRequests(t *testing.T) {
	c := &Client{run: fakeRunner("[]", nil, new([][]string))}

	if _, err := c.Stack("o", "r", 1); err == nil {
		t.Fatal("want an error when gh reports no open pull requests at all")
	}
}

func TestStackPropagatesCommandError(t *testing.T) {
	c := &Client{run: fakeRunner("", errors.New("exit status 1"), new([][]string))}

	if _, err := c.Stack("o", "r", 1); err == nil {
		t.Fatal("want an error when the gh command fails")
	}
}
