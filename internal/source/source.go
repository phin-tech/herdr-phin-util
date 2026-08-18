// Package source runs the optional external row sources that feed the project
// picker rows it could not have known about ahead of time -- a remote machine,
// a devbox, a pod, anything that is "a name and a command that puts you there".
//
// The list is live state behind whatever authenticated API the source speaks
// to, so the fetch is real work this plugin deliberately keeps outside itself:
// a source is a command that prints rows, and everything about auth,
// pagination and SDKs stays in that command. This package only knows how to
// run one, bound it in time, and read back the JSON lines it prints.
//
// Nothing here reaches Herdr or the picker. It turns a Spec into Rows (and the
// reasons some lines were dropped), and the layers above decide what to do
// with them -- which is what lets a slow or broken source degrade to "those
// rows are missing, and here is why" rather than a hung popup.
package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Spec is one configured source: a name to attribute its rows and failures to,
// the argv that produces them, and how long it is given before it is a missing
// source rather than a slow one.
type Spec struct {
	Name    string
	Command []string
	Timeout time.Duration
}

// Row is one picker row as a source prints it: a stable id for dedup and so
// Jump's ranking has something to learn against, the two lines of text the
// picker shows, the argv a pick runs to put you there, and -- optionally --
// the argv that lists what is *inside* this row when tab descends into it.
//
// The JSON tags are the wire format a source writes -- one object per line on
// stdout -- so this struct is the contract those commands are written against.
type Row struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Detail  string   `json:"detail"`
	Command []string `json:"command"`
	// Children, when set, is the command that lists this row's own rows -- a
	// remote machine's repos, a repo's branches -- printed in the same
	// JSON-lines format, recursively. tab descends into it the way it descends
	// into a repository's worktrees. A row that has Children and no Command is
	// a pure branch node: there is nothing to open, only somewhere to go.
	Children []string `json:"children"`
	// ChildrenLabel names what descending finds ("repos", "branches"), for the
	// footer that already says what tab would do. Empty is fine; the footer
	// falls back to a generic word.
	ChildrenLabel string `json:"children_label"`
}

// Result is what one source produced: its rows, and the problems worth
// surfacing rather than swallowing -- a command that failed, a timeout, a line
// that did not parse. Rows and Problems are independent: a source can print
// three good rows and one malformed line, and both the rows and the complaint
// come back.
type Result struct {
	Name     string
	Rows     []Row
	Problems []string
}

// Runner runs one source. It is a function type so the picker can be tested
// against a literal without spawning a process, and so the real implementation
// (Run) stays the only thing that touches exec.
type Runner func(Spec) Result

// Run executes a source's command, bounded by its timeout, and parses the JSON
// lines it prints. It never returns an error: a source that could not be run
// at all is a Result with a Problem and no Rows, because "this source is
// missing, here is why" is exactly the degraded state the picker is designed
// to show, not an error that would take the whole list down with it.
func Run(spec Spec) Result {
	res := Result{Name: spec.Name}
	if len(spec.Command) == 0 {
		// A source with no command cannot produce rows; config validation
		// already rejects this, so reaching here is belt and braces.
		res.Problems = append(res.Problems, fmt.Sprintf("source %q has no command", spec.Name))
		return res
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if spec.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// The rows printed before a failure are still good rows: a source that
	// paginated three pages and then timed out on the fourth should still
	// offer the three. So stdout is parsed either way, and the failure is
	// added as one more problem alongside whatever parsed.
	rows, parseProblems := parseRows(spec.Name, stdout.Bytes())
	res.Rows = rows
	res.Problems = append(res.Problems, parseProblems...)

	if err != nil {
		res.Problems = append(res.Problems, runProblem(ctx, spec, err, stderr.String()))
	}
	return res
}

// runProblem turns a command failure into the one line worth showing. A
// timeout is named as such rather than as the generic "signal: killed" the
// context cancellation surfaces, since "the source did not answer in time" is
// the actionable version and "killed" is not.
func runProblem(ctx context.Context, spec Spec, err error, stderr string) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("source %q timed out after %s", spec.Name, spec.Timeout)
	}
	msg := fmt.Sprintf("source %q failed: %v", spec.Name, err)
	if trimmed := strings.TrimSpace(stderr); trimmed != "" {
		// The command's own last words are usually the real reason; a bare exit
		// code is not something anyone can act on.
		msg += ": " + firstLine(trimmed)
	}
	return msg
}

// parseRows reads the JSON-lines a source printed, keeping the valid rows and
// reporting the rest. A blank line is skipped in silence -- it is not an error,
// just formatting -- but a non-blank line that will not parse, or one missing
// the pieces a row cannot do without, is a reported problem: a source that
// prints nine good rows and one typo should show the nine and say what was
// wrong with the tenth, the same way an unknown setup key is reported rather
// than ignored.
func parseRows(name string, out []byte) ([]Row, []string) {
	var rows []Row
	var problems []string

	scanner := bufio.NewScanner(bytes.NewReader(out))
	// A row is small, but a detail line or a long command can outgrow the
	// default 64KB token; a generous cap keeps a legitimate row from being
	// silently truncated into invalid JSON.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	line := 0
	for scanner.Scan() {
		line++
		text := bytes.TrimSpace(scanner.Bytes())
		if len(text) == 0 {
			continue
		}
		var row Row
		if err := json.Unmarshal(text, &row); err != nil {
			problems = append(problems, fmt.Sprintf("source %q line %d is not valid JSON: %v", name, line, err))
			continue
		}
		if problem := validateRow(name, line, row); problem != "" {
			problems = append(problems, problem)
			continue
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		problems = append(problems, fmt.Sprintf("source %q: reading rows: %v", name, err))
	}
	return rows, problems
}

// validateRow rejects a row that could not be acted on: one with nothing to
// show, or nothing to do at all. An id is not required here -- a row without
// one still opens -- but a row without a label would be a blank line in the
// list, and a row that is neither openable (a command) nor descendable
// (children) is an Enter and a tab that both do nothing, so those are dropped
// with a reason. A row with children and no command is legitimate: it is a
// pure branch node you descend into rather than open.
func validateRow(name string, line int, row Row) string {
	if strings.TrimSpace(row.Label) == "" {
		return fmt.Sprintf("source %q line %d has no label", name, line)
	}
	if len(row.Command) == 0 && len(row.Children) == 0 {
		return fmt.Sprintf("source %q line %d (%s) has neither a command to run nor children to descend into", name, line, row.Label)
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
