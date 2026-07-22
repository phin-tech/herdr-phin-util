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

// Notify raises a Herdr notification. Whether it lands as an in-app toast or a
// system notification is the user's choice, via [ui.toast] delivery.
func (c *Client) Notify(title, body string) error {
	params := map[string]any{"title": title}
	if body != "" {
		params["body"] = body
	}
	return c.Request("notification.show", params, nil)
}
