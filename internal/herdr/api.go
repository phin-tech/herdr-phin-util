package herdr

import (
	"fmt"
	"strings"
	"time"
)

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

// CreateTab adds a tab to an existing workspace and returns its root pane.
//
// focus is false for every tab a setup builds after the first: the tabs behind
// it are being filled in, and having the front of the Space change under
// someone while that happens is worse than arriving on tab one.
func (c *Client) CreateTab(workspaceID, cwd, label string, env map[string]string, focus bool) (Pane, string, error) {
	params := map[string]any{"focus": focus}
	if workspaceID != "" {
		params["workspace_id"] = workspaceID
	}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if label != "" {
		params["label"] = label
	}
	if len(env) > 0 {
		params["env"] = env
	}
	var res struct {
		Tab struct {
			TabID string `json:"tab_id"`
		} `json:"tab"`
		RootPane Pane `json:"root_pane"`
	}
	if err := c.Request("tab.create", params, &res); err != nil {
		return Pane{}, "", err
	}
	return res.RootPane, res.Tab.TabID, nil
}

// SplitPane splits an existing pane and returns the new one. direction is
// "right" or "down"; ratio is the new pane's share of the space, and is
// omitted when zero so Herdr's own even split applies.
func (c *Client) SplitPane(targetPaneID, direction string, ratio float64, cwd string, env map[string]string, focus bool) (Pane, error) {
	params := map[string]any{
		"target_pane_id": targetPaneID,
		"direction":      direction,
		"focus":          focus,
	}
	if ratio > 0 {
		params["ratio"] = ratio
	}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if len(env) > 0 {
		params["env"] = env
	}
	var res struct {
		Pane Pane `json:"pane"`
	}
	if err := c.Request("pane.split", params, &res); err != nil {
		return Pane{}, err
	}
	return res.Pane, nil
}

// FocusPane brings one pane to the foreground, including switching to its tab.
func (c *Client) FocusPane(paneID string) error {
	return c.Request("pane.focus", map[string]any{"pane_id": paneID}, nil)
}

// RenamePane sets the label shown on a pane's border.
func (c *Client) RenamePane(paneID, label string) error {
	return c.Request("pane.rename", map[string]any{
		"pane_id": paneID,
		"label":   label,
	}, nil)
}

// RenameTab sets a tab's label. A freshly created Space's root tab is named
// "1", so the first tab of a setup renames it rather than making a second one.
func (c *Client) RenameTab(tabID, label string) error {
	return c.Request("tab.rename", map[string]any{
		"tab_id": tabID,
		"label":  label,
	}, nil)
}

// ReadPane returns what a pane currently shows. source is "visible" for the
// on-screen rows or "recent" for recent scrollback.
func (c *Client) ReadPane(paneID, source string, lines int) (string, error) {
	var res struct {
		Read struct {
			Text string `json:"text"`
		} `json:"read"`
	}
	err := c.Request("pane.read", map[string]any{
		"pane_id": paneID,
		"source":  source,
		"lines":   lines,
	}, &res)
	if err != nil {
		return "", err
	}
	return res.Read.Text, nil
}

// SendKeys presses real keys in a pane, by Herdr's key names ("Enter").
//
// This is how a command gets submitted. A trailing "\n" inside SendText would
// not do it: send_text is a paste, and zsh's line editor inserts an embedded
// newline literally instead of running the line, leaving the command sitting
// at the prompt. Only a real key event submits.
func (c *Client) SendKeys(paneID string, keys ...string) error {
	return c.Request("pane.send_keys", map[string]any{
		"pane_id": paneID,
		"keys":    keys,
	}, nil)
}

// Command pacing. A pane exists before its shell does, and a shell exists
// before its line editor is ready, so a command typed into a fresh pane can be
// dropped in either gap. Both waits are best effort: on timeout the command is
// sent anyway, so an unusual shell degrades to the blind behaviour rather than
// hanging the whole layout.
const (
	shellReadyTimeout = 5 * time.Second
	commandEchoWait   = 5 * time.Second
	pacePollInterval  = 50 * time.Millisecond
	// echoProbeLen keeps the "did my typing land" probe on one terminal row. A
	// long command wraps, and a wrapped string never matches the rendered
	// screen, so only a short leading fragment is looked for.
	echoProbeLen = 12
)

// RunCommand types a command into a pane and submits it with a real Enter.
//
// Adapted from herdr-plus's runCommand, which learned both of these the hard
// way: wait for the shell to draw a prompt before typing, then wait for the
// typing to echo before pressing Enter. Skipping either races a freshly
// spawned shell and the command is silently lost.
func (c *Client) RunCommand(paneID, command string) error {
	c.waitForPaneReady(paneID)
	if err := c.SendText(paneID, command); err != nil {
		return err
	}
	c.waitForPaneText(paneID, echoProbe(command))
	return c.SendKeys(paneID, "Enter")
}

// echoProbe is a short, stable fragment of a command to look for on screen.
func echoProbe(command string) string {
	probe := command
	if i := strings.IndexByte(probe, '\n'); i >= 0 {
		probe = probe[:i]
	}
	if len(probe) > echoProbeLen {
		probe = probe[:echoProbeLen]
	}
	return strings.TrimSpace(probe)
}

// waitForPaneReady blocks until a pane shows anything at all, which for a
// fresh pane means its shell has printed a prompt.
func (c *Client) waitForPaneReady(paneID string) {
	deadline := time.Now().Add(shellReadyTimeout)
	for time.Now().Before(deadline) {
		if text, err := c.ReadPane(paneID, "visible", 5); err == nil && strings.TrimSpace(text) != "" {
			return
		}
		time.Sleep(pacePollInterval)
	}
}

// waitForPaneText blocks until probe appears on screen, or the wait elapses.
func (c *Client) waitForPaneText(paneID, probe string) {
	if probe == "" {
		return
	}
	deadline := time.Now().Add(commandEchoWait)
	for time.Now().Before(deadline) {
		if text, err := c.ReadPane(paneID, "visible", 20); err == nil && strings.Contains(text, probe) {
			return
		}
		time.Sleep(pacePollInterval)
	}
}

// PromptAgent submits text to an agent -- Enter included, unlike SendText.
// It is what a setup's `submit: true` pane gets.
func (c *Client) PromptAgent(paneID, text string) error {
	return c.Request("agent.prompt", map[string]any{
		"target": paneID,
		"text":   text,
	}, nil)
}

// Agent is one entry from an agent.list snapshot: a pane Herdr has recognised
// as running an agent, plus the name agent.start gave it.
type Agent struct {
	Name        string `json:"name"`
	Kind        string `json:"agent"`
	PaneID      string `json:"pane_id"`
	AgentStatus string `json:"agent_status"`

	// LaunchPending is Herdr's own "this agent was started but its launch has
	// not completed" flag, and it is the precondition agent.prompt enforces:
	// while it is set, agent.prompt rejects the target with agent_not_ready no
	// matter what the pane is showing or what agent_status says. Nothing else
	// exposed by the API reports it -- pane.list already calls the pane an
	// agent, and agent.wait will happily answer "idle".
	//
	// It says the launch handshake finished, not that the agent has drawn an
	// input: measured on codex, it clears on process detection while the
	// first-run screens are still up. So it is a necessary condition for
	// prompting rather than a sufficient one -- see readyMarkers, which is
	// what tells a drawn input from a startup screen.
	LaunchPending bool `json:"launch_pending"`
}

// Agents lists every agent Herdr currently recognises.
func (c *Client) Agents() ([]Agent, error) {
	var res struct {
		Agents []Agent `json:"agents"`
	}
	if err := c.Request("agent.list", map[string]any{}, &res); err != nil {
		return nil, err
	}
	return res.Agents, nil
}

// AgentLaunched reports whether the agent in paneID has finished launching and
// can therefore be prompted. A pane with no agent entry at all is not launched:
// detection lags agent.start by a moment, and "not there yet" and "there but
// still starting" are the same answer to the caller.
func (c *Client) AgentLaunched(paneID string) (bool, error) {
	agents, err := c.Agents()
	if err != nil {
		return false, err
	}
	for _, a := range agents {
		if a.PaneID == paneID {
			return !a.LaunchPending, nil
		}
	}
	return false, nil
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
//
// Idle is not the same as launched. Called moments after agent.start, this
// answers "idle" straight away -- an agent that has not really started yet is
// not doing anything, which is indistinguishable from an agent that is done --
// so it is a floor on readiness rather than a guarantee of it. Use
// AgentLaunched for the state agent.prompt actually requires.
func (c *Client) WaitAgentIdle(paneID string) error {
	return c.RequestWithin(waitDeadline(agentIdleTimeoutMs), "agent.wait", map[string]any{
		"target":     paneID,
		"until":      []string{"idle"},
		"timeout_ms": agentIdleTimeoutMs,
	}, nil)
}

// waitDeadline is how long to hold the connection open for a method that
// blocks server-side for timeout_ms. The slack is for the server noticing its
// own timeout and writing the answer back: without it the transport would give
// up a moment before the reply that says "timed out" arrives, and a clean
// timeout would surface as a connection error instead.
func waitDeadline(timeoutMs int) time.Duration {
	return time.Duration(timeoutMs)*time.Millisecond + defaultDeadline
}

// WaitPaneOutput blocks until value appears in the pane's current on-screen
// content, or the timeout elapses. This checks what is actually rendered,
// rather than Herdr's own activity-based agent_status guess -- which can
// report idle during a brief startup lull, before the agent has drawn
// anything a person would call ready.
func (c *Client) WaitPaneOutput(paneID, value string, timeoutMs int) error {
	return c.RequestWithin(waitDeadline(timeoutMs), "pane.wait_for_output", map[string]any{
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
