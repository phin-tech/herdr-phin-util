package open

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

func projectDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunProjectCreatesWorkspaceAtTheCheckout(t *testing.T) {
	dir := projectDir(t, "herdr-phin-util")
	s := &fakeSession{pane: herdr.Pane{PaneID: "w7:p1"}, workspaceID: "w7"}
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: false}}

	out, err := RunProject(Deps{Session: s}, cfg, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if len(s.createWorkspaceCalls) != 1 {
		t.Fatalf("expected one workspace.create, got %d", len(s.createWorkspaceCalls))
	}
	call := s.createWorkspaceCalls[0]
	if call.cwd != dir {
		t.Errorf("cwd = %q, want %q", call.cwd, dir)
	}
	if call.label != "herdr-phin-util" {
		t.Errorf("label = %q, want the directory name", call.label)
	}
	if !call.focus {
		t.Error("expected the new Space to be focused")
	}

	if out.Kind != target.KindProject {
		t.Errorf("kind = %q, want %q", out.Kind, target.KindProject)
	}
	if out.RepoPath != dir || out.WorkspaceID != "w7" || out.PaneID != "w7:p1" {
		t.Errorf("outcome did not carry the created Space: %+v", out)
	}
	// No worktree is involved in opening a checkout that already exists.
	if len(s.createWorktreeCalls) != 0 || len(s.openWorktreeCalls) != 0 {
		t.Error("expected no worktree calls")
	}
}

func TestRunProjectResolvesRelativePaths(t *testing.T) {
	dir := projectDir(t, "relative-repo")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(dir))
	defer func() { _ = os.Chdir(wd) }()

	s := &fakeSession{pane: herdr.Pane{PaneID: "w1:p1"}, workspaceID: "w1"}
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: false}}

	if _, err := RunProject(Deps{Session: s}, cfg, "relative-repo", Options{}); err != nil {
		t.Fatal(err)
	}

	got := s.createWorkspaceCalls[0].cwd
	if !filepath.IsAbs(got) {
		t.Fatalf("cwd = %q, want an absolute path", got)
	}
	// t.TempDir can sit behind a symlink (/var -> /private/var on macOS), so
	// the comparison resolves both sides rather than assuming they match.
	wantReal, _ := filepath.EvalSymlinks(dir)
	gotReal, _ := filepath.EvalSymlinks(got)
	if gotReal != wantReal {
		t.Errorf("cwd = %q, want %q", gotReal, wantReal)
	}
}

func TestRunProjectRejectsMissingDirectory(t *testing.T) {
	s := &fakeSession{}
	cfg := &config.Settings{}

	_, err := RunProject(Deps{Session: s}, cfg, filepath.Join(t.TempDir(), "nope"), Options{})
	if err == nil {
		t.Fatal("expected an error for a directory that does not exist")
	}
	if len(s.createWorkspaceCalls) != 0 {
		t.Error("nothing should be created for a path that does not resolve")
	}
}

func TestRunProjectRejectsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &fakeSession{}
	if _, err := RunProject(Deps{Session: s}, &config.Settings{}, path, Options{}); err == nil {
		t.Fatal("expected an error for a file")
	}
}

func TestRunProjectRejectsEmptyPath(t *testing.T) {
	s := &fakeSession{}
	if _, err := RunProject(Deps{Session: s}, &config.Settings{}, "", Options{}); err == nil {
		t.Fatal("expected an error for an empty path")
	}
}

// The default project prompt is empty: the agent should start with a clean
// input rather than a line of boilerplate to delete.
func TestRunProjectStartsAgentWithoutTypingByDefault(t *testing.T) {
	dir := projectDir(t, "clean-start")
	s := &fakeSession{pane: herdr.Pane{PaneID: "w2:p1"}, workspaceID: "w2"}
	cfg := &config.Settings{
		Agent:   config.AgentSettings{Enabled: true, Kind: "codex"},
		Prompts: config.PromptSettings{Project: ""},
	}

	out, err := RunProject(Deps{Session: s}, cfg, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if len(s.startAgentCalls) != 1 {
		t.Fatalf("expected the agent to be started, got %d calls", len(s.startAgentCalls))
	}
	if got := s.startAgentCalls[0].kind; got != "codex" {
		t.Errorf("agent kind = %q, want codex", got)
	}
	if len(s.sendTextCalls) != 0 {
		t.Errorf("expected nothing typed, got %+v", s.sendTextCalls)
	}
	if !out.AgentStarted {
		t.Error("AgentStarted should be true even when there is nothing to type")
	}
	if out.PromptSent != "" {
		t.Errorf("PromptSent = %q, want empty", out.PromptSent)
	}
}

func TestRunProjectTypesAConfiguredPrompt(t *testing.T) {
	dir := projectDir(t, "with-prompt")
	s := &fakeSession{pane: herdr.Pane{PaneID: "w3:p1"}, workspaceID: "w3"}
	cfg := &config.Settings{
		Agent:   config.AgentSettings{Enabled: true, Kind: "codex"},
		Prompts: config.PromptSettings{Project: "You are in {{.Repo}} at {{.Path}}"},
	}

	out, err := RunProject(Deps{Session: s}, cfg, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if len(s.sendTextCalls) != 1 {
		t.Fatalf("expected one send_text, got %d", len(s.sendTextCalls))
	}
	text := s.sendTextCalls[0].text
	if !strings.Contains(text, "with-prompt") {
		t.Errorf("prompt %q should name the repo", text)
	}
	if !strings.Contains(text, dir) {
		t.Errorf("prompt %q should carry the path", text)
	}
	if out.PromptSent != text {
		t.Errorf("PromptSent = %q, want %q", out.PromptSent, text)
	}
}

func TestRunProjectAgentToggleOverridesConfig(t *testing.T) {
	dir := projectDir(t, "toggled-off")
	s := &fakeSession{pane: herdr.Pane{PaneID: "w4:p1"}, workspaceID: "w4"}
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}}

	off := false
	if _, err := RunProject(Deps{Session: s}, cfg, dir, Options{Agent: &off}); err != nil {
		t.Fatal(err)
	}
	if len(s.startAgentCalls) != 0 {
		t.Error("--no-agent should stop the agent step")
	}
}

// A prompt made only of whitespace is the same as no prompt: sending it would
// leave stray characters in the agent's input.
func TestRunProjectSkipsWhitespaceOnlyPrompt(t *testing.T) {
	dir := projectDir(t, "whitespace")
	s := &fakeSession{pane: herdr.Pane{PaneID: "w5:p1"}, workspaceID: "w5"}
	cfg := &config.Settings{
		Agent:   config.AgentSettings{Enabled: true, Kind: "codex"},
		Prompts: config.PromptSettings{Project: "  \n  "},
	}

	if _, err := RunProject(Deps{Session: s}, cfg, dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(s.sendTextCalls) != 0 {
		t.Errorf("expected nothing typed, got %+v", s.sendTextCalls)
	}
}
