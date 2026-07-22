// Package plugin reads the invocation context Herdr injects into a plugin
// action.
package plugin

import (
	"encoding/json"
	"os"
)

// Context is the subset of HERDR_PLUGIN_CONTEXT_JSON this plugin uses.
//
// Herdr resolves FocusedPaneID against the real UI, so an action fired from a
// keybind sees the pane the user is looking at -- which is not necessarily the
// pane the action's process runs in.
type Context struct {
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceLabel string `json:"workspace_label"`
	TabID          string `json:"tab_id"`
	FocusedPaneID  string `json:"focused_pane_id"`
	FocusedPaneCwd string `json:"focused_pane_cwd"`
	InvocationSrc  string `json:"invocation_source"`
	CorrelationID  string `json:"correlation_id"`
}

// FromEnv parses the injected context. It returns nil when the variable is
// absent or unreadable, which is the normal case for a run straight from a
// shell; callers fall back to querying the session for focus.
func FromEnv() *Context {
	raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")
	if raw == "" {
		return nil
	}
	var ctx Context
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		return nil
	}
	return &ctx
}
