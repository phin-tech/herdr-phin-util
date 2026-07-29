package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/session"
)

// jumpRow is one row for the herdr-phin-jump launcher.
//
// The shape is jump's, not ours. It is a small, stable contract -- a title, a
// subtitle, and one verb naming something the launcher already knows how to do
// -- so this file is the only place the two plugins touch.
type jumpRow struct {
	Title    string     `json:"title"`
	Subtitle string     `json:"subtitle"`
	Kind     string     `json:"kind"`
	Action   jumpAction `json:"action"`
}

type jumpAction struct {
	Type    string   `json:"type"`
	Command []string `json:"command,omitempty"`
}

// runJumpRows prints this plugin's checkouts as launcher rows.
//
// It exists because the picker's list is worth having from somewhere other
// than the picker. jump has no idea where checkouts live and should not learn:
// the roots, the glob expansion, the git-only walk and the depth limit are all
// this plugin's business, and stay here.
//
// Only checkouts are emitted. session.List also reports the open Spaces, but
// jump lists those itself from the same server, and two rows going to the same
// place is worse than either alone.
//
// A failure prints an empty item list and exits zero. jump runs this while
// someone is waiting to type; a plugin that cannot answer should cost its own
// rows and nothing else -- least of all an error in a launcher the user did
// not open to hear about us.
func runJumpRows() int {
	rows := []jumpRow{}
	defer func() { emitJumpRows(rows) }()

	cfg, err := config.Load()
	if err != nil {
		return 0
	}
	client, err := herdr.New()
	if err != nil {
		return 0
	}
	candidates, err := session.List(client, cfg)
	if err != nil {
		return 0
	}

	self, err := os.Executable()
	if err != nil {
		return 0
	}

	for _, c := range candidates {
		if c.Kind != session.KindProject || c.Path == "" {
			continue
		}
		rows = append(rows, jumpRow{
			Title: c.Label,
			// What the row is, not how it reached the list. Without this jump
			// would call a checkout a "plugin", which is true of the delivery
			// and useless to the reader.
			Kind:     "project",
			Subtitle: c.Detail,
			Action: jumpAction{
				// exec rather than shell: opening a project is work done over
				// the socket, with nothing to show, so a pane would be an
				// empty box left behind as a receipt.
				Type:    "exec",
				Command: []string{self, "project", c.Path},
			},
		})
	}
	return 0
}

func emitJumpRows(rows []jumpRow) {
	payload := struct {
		Items []jumpRow `json:"items"`
	}{Items: rows}
	encoded, err := json.Marshal(payload)
	if err != nil {
		fmt.Println(`{"items":[]}`)
		return
	}
	fmt.Println(string(encoded))
}
