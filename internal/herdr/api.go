package herdr

import "fmt"

// Pane is one entry from a pane.list snapshot.
type Pane struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	Cwd         string `json:"cwd"`

	// Focused tracks the real UI focus, not the caller. It is the only
	// trustworthy way to answer "which pane is the user looking at".
	Focused bool `json:"focused"`

	// Agent is absent on panes that are not running an agent.
	Agent       *string `json:"agent"`
	AgentStatus string  `json:"agent_status"`
}

// Panes lists every live pane across every workspace.
func (c *Client) Panes() ([]Pane, error) {
	var res struct {
		Panes []Pane `json:"panes"`
	}
	if err := c.Request("pane.list", map[string]any{}, &res); err != nil {
		return nil, err
	}
	return res.Panes, nil
}

// FocusedPane returns the pane the user is currently looking at.
//
// This is deliberately not pane.current, which resolves from $HERDR_PANE_ID
// and so reports the calling pane -- for anything triggered by a keybind, that
// is the wrong pane.
func (c *Client) FocusedPane() (*Pane, error) {
	panes, err := c.Panes()
	if err != nil {
		return nil, err
	}
	for i := range panes {
		if panes[i].Focused {
			return &panes[i], nil
		}
	}
	return nil, fmt.Errorf("no focused pane")
}

// MoveToNewWorkspace relocates a pane into a brand new workspace.
//
// The terminal is moved rather than respawned, so the process keeps its PID,
// its scrollback, and -- for an agent -- its session. The pane is renumbered
// on arrival, so the returned id replaces the one that was passed in.
func (c *Client) MoveToNewWorkspace(paneID, label string, focus bool) (Pane, error) {
	dest := map[string]any{"type": "new_workspace"}
	if label != "" {
		dest["label"] = label
	}
	var res struct {
		MoveResult struct {
			Pane Pane `json:"pane"`
		} `json:"move_result"`
	}
	err := c.Request("pane.move", map[string]any{
		"pane_id":     paneID,
		"destination": dest,
		"focus":       focus,
	}, &res)
	if err != nil {
		return Pane{}, err
	}
	return res.MoveResult.Pane, nil
}

// Workspace is one entry from a workspace.list snapshot.
//
// There is no cwd here: workspace.list does not report one, so a Space's
// directory has to be recovered from the panes inside it.
type Workspace struct {
	WorkspaceID string `json:"workspace_id"`
	Number      int    `json:"number"`
	Label       string `json:"label"`
	Focused     bool   `json:"focused"`
	TabCount    int    `json:"tab_count"`
	PaneCount   int    `json:"pane_count"`
	AgentStatus string `json:"agent_status"`
}

// Workspaces lists every open Space.
func (c *Client) Workspaces() ([]Workspace, error) {
	var res struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	if err := c.Request("workspace.list", map[string]any{}, &res); err != nil {
		return nil, err
	}
	return res.Workspaces, nil
}

// FocusWorkspace brings an existing Space to the front. This is what an
// already-open project gets instead of a second Space pointed at the same
// checkout.
func (c *Client) FocusWorkspace(workspaceID string) error {
	return c.Request("workspace.focus", map[string]any{
		"workspace_id": workspaceID,
	}, nil)
}

// Worktree is one entry from a worktree.list snapshot.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Label  string `json:"label"`
	// OpenWorkspaceID is set when this worktree already has a Space, which is
	// what tells the picker to focus rather than open.
	OpenWorkspaceID string `json:"open_workspace_id"`
	// IsLinkedWorktree is false for the source checkout itself, which
	// worktree.list reports alongside the real linked worktrees.
	IsLinkedWorktree bool `json:"is_linked_worktree"`
	IsDetached       bool `json:"is_detached"`
	IsBare           bool `json:"is_bare"`
	// IsPrunable marks a worktree whose directory is gone. Opening one would
	// fail, so the picker says so rather than offering it as normal.
	IsPrunable bool `json:"is_prunable"`
}

// WorktreeSource identifies the repository a worktree list belongs to.
type WorktreeSource struct {
	RepoName string `json:"repo_name"`
	RepoRoot string `json:"repo_root"`
}

// Worktrees lists the worktrees of the repository containing cwd.
//
// The cwd parameter is what makes this usable from a picker: without it the
// server resolves the repository from the calling pane, which is the repo you
// are sitting in rather than the one you just highlighted.
func (c *Client) Worktrees(cwd string) ([]Worktree, WorktreeSource, error) {
	var res struct {
		Worktrees []Worktree     `json:"worktrees"`
		Source    WorktreeSource `json:"source"`
	}
	params := map[string]any{}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if err := c.Request("worktree.list", params, &res); err != nil {
		return nil, WorktreeSource{}, err
	}
	return res.Worktrees, res.Source, nil
}

// Notify raises a Herdr notification. Whether it lands as an in-app toast or a
// system notification is the user's choice, via [ui.toast] delivery.
func (c *Client) Notify(title, body string) error {
	params := map[string]any{"title": title}
	if body != "" {
		params["body"] = body
	}
	return c.Request("notification.show", params, nil)
}

// createResult is the shape shared by worktree.create, worktree.open and
// workspace.create: a root pane plus the workspace it landed in.
type createResult struct {
	RootPane  Pane `json:"root_pane"`
	Workspace struct {
		WorkspaceID string `json:"workspace_id"`
	} `json:"workspace"`
}

// WorktreeRequest describes a worktree to create or reopen.
type WorktreeRequest struct {
	// Cwd is an existing checkout Herdr resolves the repository from.
	Cwd string
	// Branch is the branch to check out. For worktree.create this is also
	// the name of the branch that gets made.
	Branch string
	// Base is the starting point for a new branch, e.g. "origin/main". Empty
	// leaves it to git's own default (HEAD).
	Base string
	// Path overrides where the worktree is created on disk. Empty lets
	// Herdr place it under ~/.herdr/worktrees/..., which is the right
	// default for anyone who has not opted into a specific layout.
	Path  string
	Label string
	Focus bool
}

func (r WorktreeRequest) params() map[string]any {
	p := map[string]any{
		"cwd":    r.Cwd,
		"branch": r.Branch,
		"focus":  r.Focus,
	}
	if r.Path != "" {
		p["path"] = r.Path
	}
	if r.Label != "" {
		p["label"] = r.Label
	}
	return p
}

// CreateWorktree makes a new worktree and branch, and opens it in a new
// Space. The returned pane id and workspace id are the new ones: neither
// exists until this call succeeds.
func (c *Client) CreateWorktree(req WorktreeRequest) (Pane, string, error) {
	params := req.params()
	if req.Base != "" {
		params["base"] = req.Base
	}
	var res createResult
	if err := c.Request("worktree.create", params, &res); err != nil {
		return Pane{}, "", err
	}
	return res.RootPane, res.Workspace.WorkspaceID, nil
}

// OpenWorktree opens a worktree that already exists on disk, or focuses it if
// it is already open. Use this once CreateWorktree has reported the branch is
// already checked out elsewhere.
func (c *Client) OpenWorktree(req WorktreeRequest) (Pane, string, error) {
	var res createResult
	if err := c.Request("worktree.open", req.params(), &res); err != nil {
		return Pane{}, "", err
	}
	return res.RootPane, res.Workspace.WorkspaceID, nil
}

// CreateWorkspace opens a plain Space at cwd, with no worktree involved. This
// is what a pasted string that names nothing in particular gets.
func (c *Client) CreateWorkspace(cwd, label string, focus bool) (Pane, string, error) {
	params := map[string]any{"focus": focus}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if label != "" {
		params["label"] = label
	}
	var res createResult
	if err := c.Request("workspace.create", params, &res); err != nil {
		return Pane{}, "", err
	}
	return res.RootPane, res.Workspace.WorkspaceID, nil
}

// agentIdleTimeoutMs bounds how long WaitAgentIdle waits for an agent to
// finish starting up and settle, before giving up. Some agents are slow to
// initialize; this sits comfortably under agent.wait's own 300000ms ceiling.
const agentIdleTimeoutMs = 120000

// StartAgent launches an agent in an existing pane. args is passed to the
// agent's own command line -- "--resume <id>" and nothing else, so far --
// and is omitted from the request entirely when empty, since agent.start
// treats an absent args and an empty one differently for some kinds.
func (c *Client) StartAgent(paneID, name, kind string, args []string) error {
	params := map[string]any{
		"pane_id": paneID,
		"name":    name,
		"kind":    kind,
	}
	if len(args) > 0 {
		params["args"] = args
	}
	return c.Request("agent.start", params, nil)
}

// WaitAgentIdle blocks until the agent in paneID reports idle. Typing a
// prompt before that would land on a startup banner instead of the agent's
// actual input.
func (c *Client) WaitAgentIdle(paneID string) error {
	return c.Request("agent.wait", map[string]any{
		"target":     paneID,
		"until":      []string{"idle"},
		"timeout_ms": agentIdleTimeoutMs,
	}, nil)
}

// WaitPaneOutput blocks until value appears in the pane's current on-screen
// content, or the timeout elapses. This checks what is actually rendered,
// rather than Herdr's own activity-based agent_status guess -- which can
// report idle during a brief startup lull, before the agent has drawn
// anything a person would call ready.
func (c *Client) WaitPaneOutput(paneID, value string, timeoutMs int) error {
	return c.Request("pane.wait_for_output", map[string]any{
		"pane_id":    paneID,
		"source":     "visible",
		"match":      map[string]any{"type": "substring", "value": value},
		"timeout_ms": timeoutMs,
	}, nil)
}

// SendText types text into a pane without submitting it -- deliberately
// pane.send_text rather than agent.prompt, which would press Enter on the
// user's behalf. The point is to leave a reviewed prompt sitting in the
// input for a human to send.
func (c *Client) SendText(paneID, text string) error {
	return c.Request("pane.send_text", map[string]any{
		"pane_id": paneID,
		"text":    text,
	}, nil)
}
