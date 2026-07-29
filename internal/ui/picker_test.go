package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/session"
)

func testPicker(t *testing.T, candidates ...session.Candidate) *Picker {
	t.Helper()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}}
	return NewPicker(cfg, session.Deps{}, nil, candidates)
}

func space(label, path string) session.Candidate {
	return session.Candidate{Kind: session.KindSpace, Label: label, Path: path, Detail: path, WorkspaceID: "w-" + label}
}

func project(label, path string) session.Candidate {
	return session.Candidate{Kind: session.KindProject, Label: label, Path: path, Detail: path}
}

func typeInto(p *Picker, text string) {
	for _, r := range text {
		p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func labels(candidates []session.Candidate) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.Label)
	}
	return out
}

// Rows in the same tier keep their original order, so the Spaces-then-
// projects grouping survives typing.
func TestPickerFilterPreservesOrder(t *testing.T) {
	p := testPicker(t,
		space("alpha", "/src/alpha"),
		project("alpha-tools", "/src/alpha-tools"),
		project("beta", "/src/beta"),
	)

	typeInto(p, "alpha")

	got := labels(p.filtered)
	want := []string{"alpha", "alpha-tools"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPickerFilterMatchesSubsequence(t *testing.T) {
	p := testPicker(t,
		project("herdr-phin-util", "/src/github.com/phin-tech/herdr-phin-util"),
		project("shift-clock", "/src/github.com/phin-tech/shift-clock"),
	)

	typeInto(p, "hpu")

	if len(p.filtered) != 1 || p.filtered[0].Label != "herdr-phin-util" {
		t.Errorf("got %v, want just herdr-phin-util", labels(p.filtered))
	}
}

// The detail column is a path, so filtering by where something lives works
// too.
func TestPickerFilterMatchesPath(t *testing.T) {
	p := testPicker(t,
		project("one", "/src/github.com/acme/one"),
		project("two", "/work/two"),
	)

	typeInto(p, "acme")

	if len(p.filtered) != 1 || p.filtered[0].Label != "one" {
		t.Errorf("got %v, want just one", labels(p.filtered))
	}
}

// Every project lives under the same few path components, so a subsequence
// query matches almost every path by accident. Naming something by label has
// to beat that.
func TestPickerFilterDropsPathMatchesWhenALabelMatches(t *testing.T) {
	p := testPicker(t,
		space("slack-personal-agent", "/src/github.com/phin-tech/slack-personal-agent"),
		project("orca", "/src/github.com/phin-tech/orca"),
		project("dotfiles", "/src/github.com/sam-phinizy/dotfiles"),
	)

	typeInto(p, "orca")

	if len(p.filtered) != 1 || p.filtered[0].Label != "orca" {
		t.Errorf("got %v, want just orca", labels(p.filtered))
	}
}

// Better matches sort first even though the list started the other way round.
func TestPickerFilterRanksBetterMatchesFirst(t *testing.T) {
	p := testPicker(t,
		project("odds-and-ends-recipe-archive", "/src/o"), // subsequence
		project("the-orca-book", "/src/t"),                // substring
		project("orca-tools", "/src/ot"),                  // prefix
		project("orca", "/src/orca"),                      // exact
	)

	typeInto(p, "orca")

	got := labels(p.filtered)
	want := []string{"orca", "orca-tools", "the-orca-book", "odds-and-ends-recipe-archive"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPickerFilterResetsCursor(t *testing.T) {
	p := testPicker(t, project("a", "/a"), project("b", "/b"), project("c", "/c"))

	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", p.cursor)
	}

	typeInto(p, "c")
	if p.cursor != 0 {
		t.Errorf("cursor = %d, want the filter to reset it to 0", p.cursor)
	}
}

func TestPickerFilterWithNoMatchSelectsNothing(t *testing.T) {
	p := testPicker(t, project("alpha", "/a"))

	typeInto(p, "zzzz")

	if len(p.filtered) != 0 {
		t.Fatalf("filtered = %v, want none", labels(p.filtered))
	}
	if _, ok := p.selected(); ok {
		t.Error("nothing should be selected when nothing matches")
	}
	// Enter on an empty list must be inert rather than acting on a stale row.
	if cmd := p.submit(); cmd != nil {
		t.Error("submit should do nothing with no selection")
	}
	if p.running {
		t.Error("submit should not enter the running state with no selection")
	}
}

func TestPickerCursorClampsAtBothEnds(t *testing.T) {
	p := testPicker(t, project("a", "/a"), project("b", "/b"))

	p.Update(tea.KeyMsg{Type: tea.KeyUp})
	if p.cursor != 0 {
		t.Errorf("cursor = %d, want it to stop at the top", p.cursor)
	}

	for i := 0; i < 5; i++ {
		p.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if p.cursor != 1 {
		t.Errorf("cursor = %d, want it to stop at the last row", p.cursor)
	}
}

// The scroll window has to follow the selection, or a long list selects rows
// that are not on screen.
func TestPickerScrollsToKeepSelectionVisible(t *testing.T) {
	var candidates []session.Candidate
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		candidates = append(candidates, project(name, "/src/"+name))
	}
	p := testPicker(t, candidates...)
	// A popup with room for three rows.
	p.Update(tea.WindowSizeMsg{Width: 80, Height: chromeHeight + 3})

	if got := p.pageSize(); got != 3 {
		t.Fatalf("pageSize = %d, want 3", got)
	}

	for i := 0; i < 5; i++ {
		p.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if p.cursor != 5 {
		t.Fatalf("cursor = %d, want 5", p.cursor)
	}
	if p.cursor < p.offset || p.cursor >= p.offset+p.pageSize() {
		t.Errorf("cursor %d is outside the window [%d,%d)", p.cursor, p.offset, p.offset+p.pageSize())
	}

	// And back up again.
	for i := 0; i < 5; i++ {
		p.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if p.offset != 0 {
		t.Errorf("offset = %d, want the window back at the top", p.offset)
	}
}

func TestPickerPageSizeStaysPositiveInATinyPopup(t *testing.T) {
	p := testPicker(t, project("a", "/a"))
	p.Update(tea.WindowSizeMsg{Width: 20, Height: 1})

	if got := p.pageSize(); got < 1 {
		t.Errorf("pageSize = %d, want at least 1", got)
	}
}

func TestPickerAgentToggle(t *testing.T) {
	p := testPicker(t, project("a", "/a"))
	if !p.agentOn {
		t.Fatal("agentOn should start from the config default")
	}

	p.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if p.agentOn {
		t.Error("ctrl+a should flip the toggle")
	}

	// Printable keys belong to the filter, not the toggle.
	typeInto(p, " ")
	if p.agentOn {
		t.Error("space should type into the filter, not flip the toggle")
	}
}

func TestPickerEscQuitsWithoutResult(t *testing.T) {
	p := testPicker(t, project("a", "/a"))

	p.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if _, _, _, done := p.Result(); done {
		t.Error("backing out should not report a result")
	}
	if p.View() != "" {
		t.Error("a quitting popup should render nothing")
	}
}

// While a pick is in flight nothing may change the fields it is reading.
func TestPickerIgnoresInputWhileRunning(t *testing.T) {
	p := testPicker(t, project("a", "/a"), project("b", "/b"))
	p.running = true

	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.cursor != 0 {
		t.Error("cursor should not move while a pick is running")
	}

	p.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if !p.agentOn {
		t.Error("the toggle should not flip while a pick is running")
	}

	p.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, Y: p.toggleRow})
	if !p.agentOn {
		t.Error("clicks should be ignored while a pick is running")
	}
}

func TestPickerClickSelectsARow(t *testing.T) {
	p := testPicker(t, project("a", "/a"), project("b", "/b"), project("c", "/c"))
	// Render once so the hit regions exist.
	p.View()

	p.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, Y: p.listTop + 2})

	if p.cursor != 2 {
		t.Errorf("cursor = %d, want the clicked row", p.cursor)
	}
	if !p.running {
		t.Error("clicking a row should act on it, not just select it")
	}
}

func TestPickerClickBelowTheLastRowDoesNothing(t *testing.T) {
	p := testPicker(t, project("a", "/a"))
	p.View()

	p.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, Y: p.listTop + 5})

	if p.running {
		t.Error("clicking empty space should not act on anything")
	}
}

// The wheel moves the selection, so the highlighted row is always the one
// Enter would act on.
func TestPickerWheelMovesTheSelection(t *testing.T) {
	var candidates []session.Candidate
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		candidates = append(candidates, project(name, "/src/"+name))
	}
	p := testPicker(t, candidates...)
	p.Update(tea.WindowSizeMsg{Width: 80, Height: chromeHeight + 2})

	p.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	p.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})

	if p.cursor != 2 {
		t.Errorf("cursor = %d, want 2", p.cursor)
	}
	if p.cursor < p.offset || p.cursor >= p.offset+p.pageSize() {
		t.Errorf("cursor %d is outside the visible window [%d,%d)", p.cursor, p.offset, p.offset+p.pageSize())
	}
	if p.running {
		t.Error("the wheel should not act on a row")
	}

	p.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	if p.cursor != 1 {
		t.Errorf("cursor = %d, want 1", p.cursor)
	}
}

func TestPickerViewShowsBothKinds(t *testing.T) {
	p := testPicker(t, space("running", "/src/running"), project("idle", "/src/idle"))
	out := p.View()

	if !strings.Contains(out, "running") || !strings.Contains(out, "idle") {
		t.Errorf("view should list both rows:\n%s", out)
	}
	if !strings.Contains(out, "open") || !strings.Contains(out, "new") {
		t.Errorf("view should tag which is which:\n%s", out)
	}
}

func TestPickerViewReportsAnEmptyList(t *testing.T) {
	p := testPicker(t)
	if !strings.Contains(p.View(), "roots") {
		t.Errorf("an empty picker should point at the config:\n%s", p.View())
	}
}

func TestPickerCountSummary(t *testing.T) {
	p := testPicker(t, project("a", "/a"), project("b", "/b"))
	if got := p.countSummary(); got != "2 total" {
		t.Errorf("got %q, want \"2 total\"", got)
	}

	typeInto(p, "a")
	if got := p.countSummary(); got != "1 of 2" {
		t.Errorf("got %q, want \"1 of 2\"", got)
	}

	typeInto(p, "zzz")
	if got := p.countSummary(); got != "no match" {
		t.Errorf("got %q, want \"no match\"", got)
	}
}

// A truncated path has to keep its tail: ".../acme/repo" identifies a checkout
// and "/Users/someone/src/gith..." does not.
func TestTruncateKeepsTheTail(t *testing.T) {
	const path = "/Users/someone/src/github.com/acme/repo"

	got := truncate(path, 14)
	if !strings.HasSuffix(got, "acme/repo") {
		t.Errorf("got %q, want it to end in the checkout", got)
	}
	if len([]rune(got)) > 14 {
		t.Errorf("got %q, longer than the %d requested", got, 14)
	}

	if got := truncate("short", 20); got != "short" {
		t.Errorf("got %q, want the string untouched", got)
	}
}

// A name clipped to the column loses its tail, not its head: repo names
// differ least at the end.
func TestTruncateTailKeepsTheHead(t *testing.T) {
	got := truncateTail("roux-library-sync-fixture", 10)
	if !strings.HasPrefix(got, "roux") {
		t.Errorf("got %q, want it to start with the name", got)
	}
	if len([]rune(got)) > 10 {
		t.Errorf("got %q, longer than the 10 requested", got)
	}
	if got := truncateTail("short", 20); got != "short" {
		t.Errorf("got %q, want the string untouched", got)
	}
}

// The paths have to line up, or the column cannot be read down.
func TestPickerAlignsTheDetailColumn(t *testing.T) {
	p := testPicker(t,
		project("a", "/src/a"),
		project("a-much-longer-name", "/src/a-much-longer-name"),
	)
	p.Update(tea.WindowSizeMsg{Width: 90, Height: 20})

	rows := p.viewRows()
	// Measured as display width, not bytes: the selection marker is multibyte
	// and the styling adds escapes, neither of which occupies a column.
	first := detailColumn(rows[0], "/src/a")
	second := detailColumn(rows[1], "/src/a")
	if first != second {
		t.Errorf("paths start at column %d and %d, want the same:\n%s\n%s", first, second, rows[0], rows[1])
	}
}

// detailColumn reports the screen column the detail text begins at.
func detailColumn(row, detail string) int {
	i := strings.Index(row, detail)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(row[:i])
}

func TestPlainLabelWidthCountsTheCurrentMarker(t *testing.T) {
	plain := project("repo", "/src/repo")
	current := session.Candidate{Kind: session.KindSpace, Label: "repo", Focused: true}

	if got, want := plainLabelWidth(plain), 4; got != want {
		t.Errorf("got %d, want %d", got, want)
	}
	if got, want := plainLabelWidth(current), 4+len(currentSuffix); got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestStatusForNamesTheAction(t *testing.T) {
	if got := statusFor(space("alpha", "/a")); !strings.Contains(got, "switching") {
		t.Errorf("got %q, want it to say switching", got)
	}
	if got := statusFor(project("beta", "/b")); !strings.Contains(got, "opening") {
		t.Errorf("got %q, want it to say opening", got)
	}
}

func TestSubsequence(t *testing.T) {
	cases := []struct {
		query, text string
		want        bool
	}{
		{"", "anything", true},
		{"abc", "a-b-c", true},
		{"abc", "cba", false},
		{"abc", "ab", false},
		// Runes, not bytes: an accent must not split the walk.
		{"é", "café", true},
		{"cé", "café", true},
	}
	for _, tc := range cases {
		if got := subsequence(tc.query, tc.text); got != tc.want {
			t.Errorf("subsequence(%q, %q) = %v, want %v", tc.query, tc.text, got, tc.want)
		}
	}
}
