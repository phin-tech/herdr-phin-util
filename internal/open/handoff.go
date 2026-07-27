package open

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/phin-tech/herdr-phin-util/internal/claudesess"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// handoffKind is fixed rather than read from cfg.Agent.Kind: a Claude session
// id means nothing to codex or gemini, so letting the config choose the agent
// would only produce a confident, broken resume.
const handoffKind = "claude"

// HandoffOptions is what the caller knows and this package does not.
type HandoffOptions struct {
	// Cwd is the directory the source session is running in. Empty means the
	// process's own working directory, which is the usual case: the command
	// is run from the terminal being handed off.
	Cwd string
	// SessionID skips resolution entirely. This is the escape hatch for
	// resuming a session other than the current one.
	SessionID string
	// Label names the Space. Empty falls back to the directory's base name,
	// the same convention RunProject uses.
	Label string
	// Home is where ~/.claude lives, injected for tests. Empty means the real
	// user's home directory.
	Home string
}

// RunHandoff opens a Space at cwd and resumes an existing Claude session in
// it.
//
// This is not a move, and cannot be: Herdr can only relocate panes it already
// owns, so a session started in a plain terminal has no live process to
// carry. What survives is the transcript -- a fresh `claude --resume` against
// the same session file -- which means the caller has to quit the original.
// Two processes appending to one session file will diverge.
func RunHandoff(deps Deps, opts HandoffOptions) (Outcome, error) {
	plan, err := PlanHandoff(deps, opts)
	if err != nil {
		return Outcome{}, err
	}

	tgt := target.Target{Kind: target.KindProject, Text: plan.Label}

	pane, workspaceID, err := deps.Session.CreateWorkspace(plan.Cwd, tgt.Label(), true)
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{
		Kind:        tgt.Kind,
		Label:       tgt.Label(),
		RepoPath:    plan.Cwd,
		WorkspaceID: workspaceID,
		PaneID:      pane.PaneID,

		SessionID:      plan.SessionID,
		SessionWidened: plan.Widened,
		SessionModTime: plan.ModTime,
	}

	// runAgentStep is deliberately not reused. It exists to type a rendered
	// prompt into a fresh agent, and a resumed session already has its
	// content -- typing a project template into it would put words in the
	// mouth of a conversation that is mid-flow.
	args := []string{"--resume", plan.SessionID}
	if err := startAgentWithRetry(deps.Session, pane.PaneID, workspaceID, agentName(tgt.Label()), handoffKind, args); err != nil {
		return out, fmt.Errorf("start agent: %w", err)
	}
	if err := deps.Session.WaitAgentIdle(pane.PaneID); err != nil {
		return out, fmt.Errorf("wait for agent: %w", err)
	}
	if marker, ok := readyMarkers[handoffKind]; ok {
		if err := deps.Session.WaitPaneOutput(pane.PaneID, marker, readyMarkerTimeoutMs); err != nil {
			return out, fmt.Errorf("wait for agent to render its prompt: %w", err)
		}
	}

	out.AgentStarted = true
	return out, nil
}

// HandoffPlan is what a handoff would do, decided but not yet done.
type HandoffPlan struct {
	SessionID string
	// Cwd is where the Space will open, which is the session's own directory
	// whenever the search had to widen to find it.
	Cwd   string
	Label string
	// Widened means the session came from outside the directory the command
	// was run from -- a guess, and one worth showing the user.
	Widened bool
	ModTime time.Time
}

// PlanHandoff resolves which session a handoff would resume and where it
// would open it, touching nothing.
//
// Separating this from RunHandoff is what makes the widened search safe to
// offer: a guess that can be previewed is a much easier guess to live with
// than one you only discover by watching a Space appear.
func PlanHandoff(deps Deps, opts HandoffOptions) (HandoffPlan, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = deps.Cwd
	}
	if cwd == "" {
		return HandoffPlan{}, fmt.Errorf("no working directory to hand off from")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return HandoffPlan{}, fmt.Errorf("resolve %s: %w", cwd, err)
	}

	found, err := resolveSession(opts, abs)
	if err != nil {
		return HandoffPlan{}, err
	}
	// A widened search answers with a session from somewhere else entirely,
	// and the Space has to follow it. Opening a conversation about one
	// repository in the directory of another would be worse than not finding
	// it at all.
	if found.session.Cwd != "" {
		abs = found.session.Cwd
	}

	label := opts.Label
	if label == "" {
		label = filepath.Base(abs)
	}

	return HandoffPlan{
		SessionID: found.session.ID,
		Cwd:       abs,
		Label:     label,
		Widened:   found.widened,
		ModTime:   found.session.ModTime,
	}, nil
}

// resolved is a session and how much searching it took to find, so the caller
// can say so when the answer came from further away than the caller stands.
type resolved struct {
	session claudesess.Session
	// widened means the session was found outside the working directory the
	// command was run from, and its own cwd is what the Space will use.
	widened bool
}

// resolveSession works outwards: what it is told, what the environment knows,
// what is on disk here, and finally what is on disk anywhere.
//
// The environment beats disk because it names the actual session rather than
// the most recently touched one. Disk-here beats disk-anywhere because a
// session in the directory you are standing in is almost certainly the one
// you meant, and only when there is no such session is it worth guessing
// across the whole machine.
func resolveSession(opts HandoffOptions, cwd string) (resolved, error) {
	if opts.SessionID != "" {
		return resolved{session: claudesess.Session{ID: opts.SessionID}}, nil
	}
	if id := claudesess.FromEnv(); id != "" {
		return resolved{session: claudesess.Session{ID: id}}, nil
	}

	home := opts.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return resolved{}, fmt.Errorf("no %s set and no home directory to search for one", claudesess.EnvVar)
	}

	if s, err := claudesess.Latest(home, cwd); err == nil {
		return resolved{session: s}, nil
	}

	s, err := claudesess.LatestAny(home)
	if err != nil {
		return resolved{}, fmt.Errorf("%w -- run this from inside the session you want to hand off, or pass --session", err)
	}
	return resolved{session: s, widened: true}, nil
}
