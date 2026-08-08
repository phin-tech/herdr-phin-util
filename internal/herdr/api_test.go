package herdr

import "testing"

// worktree.open rejects a request carrying both path and branch, or neither
// -- it wants exactly one. This pins that contract against regression: it's
// what broke reopening an existing worktree once already.
func TestWorktreeRequestOpenParamsExactlyOneOfPathOrBranch(t *testing.T) {
	cases := []struct {
		name       string
		req        WorktreeRequest
		wantPath   string
		wantBranch string
	}{
		{
			name:       "path known (Existing worktree, or a resolved [worktrees].path)",
			req:        WorktreeRequest{Cwd: "/repo", Branch: "feature/x", Path: "/repo/../worktrees/feature-x"},
			wantPath:   "/repo/../worktrees/feature-x",
			wantBranch: "",
		},
		{
			name:       "path unknown (no [worktrees].path configured)",
			req:        WorktreeRequest{Cwd: "/repo", Branch: "feature/x"},
			wantPath:   "",
			wantBranch: "feature/x",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.req.openParams()

			_, hasPath := p["path"]
			_, hasBranch := p["branch"]
			if hasPath && hasBranch {
				t.Fatalf("openParams() set both path and branch: %#v", p)
			}
			if !hasPath && !hasBranch {
				t.Fatalf("openParams() set neither path nor branch: %#v", p)
			}

			if tc.wantPath != "" {
				if got, _ := p["path"].(string); got != tc.wantPath {
					t.Errorf("path = %q, want %q", got, tc.wantPath)
				}
			}
			if tc.wantBranch != "" {
				if got, _ := p["branch"].(string); got != tc.wantBranch {
					t.Errorf("branch = %q, want %q", got, tc.wantBranch)
				}
			}
		})
	}
}

// worktree.create tolerates both path and branch together -- CreateWorktree
// keeps using the shared params(), unlike OpenWorktree.
func TestWorktreeRequestParamsAllowsBothPathAndBranch(t *testing.T) {
	req := WorktreeRequest{Cwd: "/repo", Branch: "feature/x", Path: "/repo/../worktrees/feature-x"}
	p := req.params()

	if p["branch"] != "feature/x" {
		t.Errorf("branch = %v, want feature/x", p["branch"])
	}
	if p["path"] != "/repo/../worktrees/feature-x" {
		t.Errorf("path = %v, want the worktree path", p["path"])
	}
}
