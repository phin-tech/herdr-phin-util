package open

import (
	"fmt"
	"strconv"

	"github.com/phin-tech/herdr-phin-util/internal/gh"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// stackLayers turns a resolved pull request stack into the "layers" list a
// for_each tab reads from ([setup.Data].Lists), one map[string]string per
// layer, bottom of the stack first -- exactly gh.Client.Stack's own order,
// since there is no reason to disagree with the package that did the actual
// walk.
//
// base_pr is the PR number immediately below this layer in the chain, empty
// for the bottom layer -- it bases on the trunk, not on another open PR --
// which is just the previous element of this same slice, since Stack has
// already done the harder work of ordering it correctly.
//
// layer is 1-based and set explicitly, alongside the {{.layer_index}} that
// tabIterations already adds for free to every for_each element; both exist
// on purpose (see issue #13) rather than making a setup reach for one and
// hope it means the other.
//
// This alone is what makes for_each usable -- nothing more. There is
// deliberately no "worktree" field here: giving each layer its own checkout
// needs a tab that can pin itself to a ref, which is issue #12 (`worktree:`
// on a tab) and is not built yet. Until it lands, a for_each tab over
// "layers" shares the Space's one cwd like any other tab reviewing a single
// PR -- a field promising a directory nothing creates would be worse than no
// field at all.
func stackLayers(stack []gh.StackPR) []map[string]string {
	out := make([]map[string]string, len(stack))
	for i, pr := range stack {
		basePR := ""
		if i > 0 {
			basePR = strconv.Itoa(stack[i-1].Number)
		}
		out[i] = map[string]string{
			"layer":       strconv.Itoa(i + 1),
			"pr":          strconv.Itoa(pr.Number),
			"title":       pr.Title,
			"url":         pr.URL,
			"head_branch": pr.HeadBranch,
			"head_sha":    pr.HeadSHA,
			"base_branch": pr.BaseBranch,
			"base_pr":     basePR,
		}
	}
	return out
}

// resolveLists builds a [setup.Data].Lists for whichever list names the
// chosen setup actually asked for -- def.ForEachNames(), never more. Fetching
// a stack is a gh pr list call, a network round trip, and every setup that
// never writes for_each -- most of them -- must not pay for it just because
// the target it was applied to happens to be a pull request.
//
// Only github_pr resolves anything today, and only "layers", which is the
// one list source this issue builds. A second source later ("packages",
// "failing_tests", a repo's open review threads, ...) slots in here behind
// the same "did anyone actually ask for this?" check -- one more clause
// alongside this one, nothing about the shape here is specific to stacks.
func resolveLists(prs PRLookup, tgt target.Target, names []string) (map[string][]map[string]string, error) {
	if tgt.Kind != target.KindGitHubPR || !containsName(names, "layers") {
		return nil, nil
	}
	stack, err := prs.Stack(tgt.Owner, tgt.Repo, tgt.Number)
	if err != nil {
		return nil, fmt.Errorf("resolve layers: %w", err)
	}
	return map[string][]map[string]string{"layers": stackLayers(stack)}, nil
}

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
