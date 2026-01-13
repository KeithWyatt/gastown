package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

var stateJSON bool

var stateCmd = &cobra.Command{
	Use:     "state",
	GroupID: GroupDiag,
	Short:   "Show current agent state (introspection API)",
	Long: `Display comprehensive state information for the current agent.

This command provides a unified introspection API that combines:
- Role detection (who am I?)
- Session state (normal, post-handoff, crash-recovery, autonomous)
- Runtime status (is my tmux session running?)
- Hook status (what work is on my hook?)
- Mail status (unread message count)
- Agent bead state (operational labels like idle count)

Use --json for machine-readable output suitable for scripting and automation.

EXAMPLES:
  gt state              # Human-readable state summary
  gt state --json       # JSON output for scripting
  gt state --json | jq '.hook.bead_id'  # Extract specific fields`,
	RunE: runState,
}

func init() {
	stateCmd.Flags().BoolVar(&stateJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(stateCmd)
}

// StateInfo represents comprehensive agent state for introspection.
type StateInfo struct {
	// Identity
	Role       string `json:"role"`
	Rig        string `json:"rig,omitempty"`
	Worker     string `json:"worker,omitempty"` // Polecat or crew member name
	Address    string `json:"address"`          // Full agent address (e.g., "gastown/polecats/Toast")
	AgentBead  string `json:"agent_bead"`       // Agent bead ID (e.g., "gt-gastown-polecat-Toast")
	RoleSource string `json:"role_source"`      // How role was detected: "env" or "cwd"

	// Locations
	TownRoot string `json:"town_root"`
	WorkDir  string `json:"work_dir"`
	Home     string `json:"home"` // Canonical home directory for this role

	// Session lifecycle state
	SessionState string `json:"session_state"` // normal, post-handoff, crash-recovery, autonomous

	// Runtime status
	Runtime RuntimeState `json:"runtime"`

	// Hook (pinned work)
	Hook HookState `json:"hook"`

	// Mail
	Mail MailState `json:"mail"`

	// Agent operational state (labels from agent bead)
	Labels map[string]string `json:"labels,omitempty"`
}

// RuntimeState represents tmux session runtime info.
type RuntimeState struct {
	Session string `json:"session"`        // tmux session name
	Running bool   `json:"running"`        // Is session alive?
	Pane    string `json:"pane,omitempty"` // Current pane ID
}

// HookState represents the agent's hook (pinned work).
type HookState struct {
	HasWork bool   `json:"has_work"`
	BeadID  string `json:"bead_id,omitempty"`
	Title   string `json:"title,omitempty"`
	Status  string `json:"status,omitempty"` // hooked, in_progress, etc.
}

// MailState represents mail inbox status.
type MailState struct {
	UnreadCount  int    `json:"unread_count"`
	FirstSubject string `json:"first_subject,omitempty"`
}

func runState(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}

	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Get role info
	roleInfo, err := GetRoleWithContext(cwd, townRoot)
	if err != nil {
		return fmt.Errorf("detecting role: %w", err)
	}

	// Build state info
	state := StateInfo{
		Role:       string(roleInfo.Role),
		Rig:        roleInfo.Rig,
		Worker:     roleInfo.Polecat,
		Address:    roleInfo.ActorString(),
		RoleSource: roleInfo.Source,
		TownRoot:   townRoot,
		WorkDir:    cwd,
		Home:       roleInfo.Home,
	}

	// Get agent bead ID
	ctx := RoleContext{
		Role:     roleInfo.Role,
		Rig:      roleInfo.Rig,
		Polecat:  roleInfo.Polecat,
		TownRoot: townRoot,
		WorkDir:  cwd,
	}
	state.AgentBead = getAgentBeadID(ctx)

	// Detect session state
	sessionState := detectSessionState(ctx)
	state.SessionState = sessionState.State

	// Get runtime status (tmux session)
	state.Runtime = getRuntimeState(roleInfo, townRoot)

	// Get hook status
	state.Hook = getHookState(ctx, state.AgentBead, townRoot)

	// Get mail status
	state.Mail = getMailState(roleInfo.ActorString(), townRoot)

	// Get agent bead labels (operational state)
	state.Labels = getStateLabels(state.AgentBead, cwd)

	// Output
	if stateJSON {
		return outputStateJSON(state)
	}
	return outputStateText(state)
}

func outputStateJSON(state StateInfo) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(state)
}

func outputStateText(state StateInfo) error {
	// Header
	fmt.Printf("%s %s\n", style.Bold.Render("Role:"), state.Role)
	if state.Rig != "" {
		fmt.Printf("%s %s\n", style.Bold.Render("Rig:"), state.Rig)
	}
	if state.Worker != "" {
		fmt.Printf("%s %s\n", style.Bold.Render("Worker:"), state.Worker)
	}
	fmt.Printf("%s %s\n", style.Bold.Render("Address:"), state.Address)
	fmt.Printf("%s %s\n", style.Bold.Render("Agent Bead:"), state.AgentBead)
	fmt.Printf("%s %s\n", style.Bold.Render("Detection:"), state.RoleSource)
	fmt.Println()

	// Locations
	fmt.Printf("%s\n", style.Dim.Render("Locations:"))
	fmt.Printf("  Town Root: %s\n", state.TownRoot)
	fmt.Printf("  Work Dir:  %s\n", state.WorkDir)
	fmt.Printf("  Home:      %s\n", state.Home)
	fmt.Println()

	// Session State
	fmt.Printf("%s %s\n", style.Bold.Render("Session State:"), formatSessionState(state.SessionState))
	fmt.Println()

	// Runtime
	fmt.Printf("%s\n", style.Dim.Render("Runtime:"))
	fmt.Printf("  Session: %s\n", state.Runtime.Session)
	if state.Runtime.Running {
		fmt.Printf("  Status:  %s\n", style.Success.Render("running"))
	} else {
		fmt.Printf("  Status:  %s\n", style.Error.Render("stopped"))
	}
	if state.Runtime.Pane != "" {
		fmt.Printf("  Pane:    %s\n", state.Runtime.Pane)
	}
	fmt.Println()

	// Hook
	fmt.Printf("%s\n", style.Dim.Render("Hook:"))
	if state.Hook.HasWork {
		fmt.Printf("  Bead:   %s\n", state.Hook.BeadID)
		fmt.Printf("  Title:  %s\n", state.Hook.Title)
		fmt.Printf("  Status: %s\n", state.Hook.Status)
	} else {
		fmt.Printf("  %s\n", style.Dim.Render("(no work on hook)"))
	}
	fmt.Println()

	// Mail
	fmt.Printf("%s\n", style.Dim.Render("Mail:"))
	if state.Mail.UnreadCount > 0 {
		fmt.Printf("  Unread: %d\n", state.Mail.UnreadCount)
		if state.Mail.FirstSubject != "" {
			fmt.Printf("  Latest: %s\n", state.Mail.FirstSubject)
		}
	} else {
		fmt.Printf("  %s\n", style.Dim.Render("(no unread mail)"))
	}
	fmt.Println()

	// Labels
	if len(state.Labels) > 0 {
		fmt.Printf("%s\n", style.Dim.Render("Operational Labels:"))
		for key, value := range state.Labels {
			fmt.Printf("  %s: %s\n", key, value)
		}
	}

	return nil
}

func formatSessionState(state string) string {
	switch state {
	case "normal":
		return style.Success.Render(state)
	case "autonomous":
		return style.Warning.Render(state + " (work on hook)")
	case "post-handoff":
		return style.Warning.Render(state)
	case "crash-recovery":
		return style.Error.Render(state)
	default:
		return state
	}
}

// getRuntimeState gets tmux session runtime info for the agent.
func getRuntimeState(roleInfo RoleInfo, townRoot string) RuntimeState {
	state := RuntimeState{}

	// Determine session name based on role
	switch roleInfo.Role {
	case RoleMayor:
		state.Session = getMayorSessionName()
	case RoleDeacon:
		state.Session = getDeaconSessionName()
	case RoleWitness:
		state.Session = witnessSessionName(roleInfo.Rig)
	case RoleRefinery:
		state.Session = fmt.Sprintf("gt-%s-refinery", roleInfo.Rig)
	case RolePolecat:
		state.Session = fmt.Sprintf("gt-%s-%s", roleInfo.Rig, roleInfo.Polecat)
	case RoleCrew:
		state.Session = crewSessionName(roleInfo.Rig, roleInfo.Polecat)
	default:
		// Try to detect from tmux environment
		state.Session = os.Getenv("TMUX_PANE")
	}

	// Check if session is running
	t := tmux.NewTmux()
	if state.Session != "" {
		has, _ := t.HasSession(state.Session)
		state.Running = has
	}

	// Get current pane if in tmux
	state.Pane = os.Getenv("TMUX_PANE")

	return state
}

// getHookState gets the agent's hook (pinned work) status.
func getHookState(ctx RoleContext, agentBeadID, townRoot string) HookState {
	state := HookState{}

	if agentBeadID == "" {
		return state
	}

	// Determine beads path based on role
	var beadsPath string
	switch ctx.Role {
	case RoleMayor, RoleDeacon:
		beadsPath = beads.GetTownBeadsPath(townRoot)
	default:
		if ctx.Rig != "" {
			beadsPath = filepath.Join(townRoot, ctx.Rig, "mayor", "rig")
		} else {
			beadsPath = ctx.WorkDir
		}
	}

	// Look up agent bead to get hook info
	b := beads.New(beadsPath)
	issues, err := b.ShowMultiple([]string{agentBeadID})
	if err != nil || len(issues) == 0 {
		return state
	}

	agentIssue := issues[agentBeadID]
	if agentIssue == nil {
		return state
	}

	// Get hook bead ID from agent bead
	hookBeadID := agentIssue.HookBead
	if hookBeadID == "" {
		// Try parsing from description for legacy beads
		fields := beads.ParseAgentFields(agentIssue.Description)
		if fields != nil {
			hookBeadID = fields.HookBead
		}
	}

	if hookBeadID == "" {
		return state
	}

	// Fetch hook bead details
	hookIssues, err := b.ShowMultiple([]string{hookBeadID})
	if err != nil || len(hookIssues) == 0 {
		// Hook bead reference exists but can't fetch details
		state.HasWork = true
		state.BeadID = hookBeadID
		return state
	}

	hookIssue := hookIssues[hookBeadID]
	if hookIssue != nil {
		state.HasWork = true
		state.BeadID = hookBeadID
		state.Title = hookIssue.Title
		state.Status = hookIssue.Status
	}

	return state
}

// getMailState gets the agent's mail inbox status.
func getMailState(address, townRoot string) MailState {
	state := MailState{}

	router := mail.NewRouter(townRoot)
	mailbox, err := router.GetMailbox(address)
	if err != nil {
		return state
	}

	_, unread, _ := mailbox.Count()
	state.UnreadCount = unread

	if unread > 0 {
		messages, err := mailbox.ListUnread()
		if err == nil && len(messages) > 0 {
			state.FirstSubject = messages[0].Subject
		}
	}

	return state
}

// getStateLabels retrieves operational state labels from the agent bead.
// Uses the existing getAgentLabels helper from agent_state.go.
func getStateLabels(agentBeadID, workDir string) map[string]string {
	if agentBeadID == "" {
		return nil
	}

	beadsDir := beads.ResolveBeadsDir(workDir)
	if beadsDir == "" {
		return nil
	}

	// Use the existing helper from agent_state.go
	labels, err := getAgentLabels(agentBeadID, beadsDir)
	if err != nil {
		return nil
	}

	return labels
}
