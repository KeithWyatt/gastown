package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
)

// Polecat command flags
var (
	polecatListJSON  bool
	polecatListAll   bool
	polecatForce     bool
	polecatRemoveAll bool
)

var polecatCmd = &cobra.Command{
	Use:     "polecat",
	Aliases: []string{"cat", "polecats"},
	Short:   "Manage polecats in rigs",
	Long: `Manage polecat lifecycle in rigs.

Polecats are worker agents that operate in their own git clones.
Use the subcommands to add, remove, list, wake, and sleep polecats.`,
}

var polecatListCmd = &cobra.Command{
	Use:   "list [rig]",
	Short: "List polecats in a rig",
	Long: `List polecats in a rig or all rigs.

In the transient model, polecats exist only while working. The list shows
all currently active polecats with their states:
  - working: Actively working on an issue
  - done: Completed work, waiting for cleanup
  - stuck: Needs assistance

Examples:
  gt polecat list gastown
  gt polecat list --all
  gt polecat list gastown --json`,
	RunE: runPolecatList,
}

var polecatAddCmd = &cobra.Command{
	Use:   "add <rig> <name>",
	Short: "Add a new polecat to a rig",
	Long: `Add a new polecat to a rig.

Creates a polecat directory, clones the rig repo, creates a work branch,
and initializes state.

Example:
  gt polecat add gastown Toast`,
	Args: cobra.ExactArgs(2),
	RunE: runPolecatAdd,
}

var polecatRemoveCmd = &cobra.Command{
	Use:   "remove <rig>/<polecat>... | <rig> --all",
	Short: "Remove polecats from a rig",
	Long: `Remove one or more polecats from a rig.

Fails if session is running (stop first).
Warns if uncommitted changes exist.
Use --force to bypass checks.

Examples:
  gt polecat remove gastown/Toast
  gt polecat remove gastown/Toast gastown/Furiosa
  gt polecat remove gastown --all
  gt polecat remove gastown --all --force`,
	Args: cobra.MinimumNArgs(1),
	RunE: runPolecatRemove,
}

var polecatWakeCmd = &cobra.Command{
	Use:   "wake <rig>/<polecat>",
	Short: "(Deprecated) Resume a polecat to working state",
	Long: `Resume a polecat to working state.

DEPRECATED: In the transient model, polecats are created fresh for each task
via 'gt spawn'. This command is kept for backward compatibility.

Transitions: done → working

Example:
  gt polecat wake gastown/Toast`,
	Args: cobra.ExactArgs(1),
	RunE: runPolecatWake,
}

var polecatSleepCmd = &cobra.Command{
	Use:   "sleep <rig>/<polecat>",
	Short: "(Deprecated) Mark polecat as done",
	Long: `Mark polecat as done.

DEPRECATED: In the transient model, polecats use 'gt handoff' when complete,
which triggers automatic cleanup by the Witness. This command is kept for
backward compatibility.

Transitions: working → done

Example:
  gt polecat sleep gastown/Toast`,
	Args: cobra.ExactArgs(1),
	RunE: runPolecatSleep,
}

var polecatDoneCmd = &cobra.Command{
	Use:     "done <rig>/<polecat>",
	Aliases: []string{"finish"},
	Short:   "Mark polecat as done with work and return to idle",
	Long: `Mark polecat as done with work and return to idle.

Transitions: working/done/stuck → idle
Clears the assigned issue.
Fails if session is running (stop first).

Example:
  gt polecat done gastown/Toast
  gt polecat finish gastown/Toast`,
	Args: cobra.ExactArgs(1),
	RunE: runPolecatDone,
}

var polecatResetCmd = &cobra.Command{
	Use:   "reset <rig>/<polecat>",
	Short: "Force reset polecat to idle state",
	Long: `Force reset polecat to idle state.

Transitions: any state → idle
Clears the assigned issue.
Use when polecat is stuck in an unexpected state.
Fails if session is running (stop first).

Example:
  gt polecat reset gastown/Toast`,
	Args: cobra.ExactArgs(1),
	RunE: runPolecatReset,
}

var polecatSyncCmd = &cobra.Command{
	Use:   "sync <rig>/<polecat>",
	Short: "Sync beads for a polecat",
	Long: `Sync beads for a polecat's worktree.

Runs 'bd sync' in the polecat's worktree to push local beads changes
to the shared sync branch and pull remote changes.

Use --all to sync all polecats in a rig.
Use --from-main to only pull (no push).

Examples:
  gt polecat sync gastown/Toast
  gt polecat sync gastown --all
  gt polecat sync gastown/Toast --from-main`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPolecatSync,
}

var polecatStatusCmd = &cobra.Command{
	Use:   "status <rig>/<polecat>",
	Short: "Show detailed status for a polecat",
	Long: `Show detailed status for a polecat.

Displays comprehensive information including:
  - Current lifecycle state (working, done, stuck, idle)
  - Assigned issue (if any)
  - Session status (running/stopped, attached/detached)
  - Session creation time
  - Last activity time

Examples:
  gt polecat status gastown/Toast
  gt polecat status gastown/Toast --json`,
	Args: cobra.ExactArgs(1),
	RunE: runPolecatStatus,
}

var (
	polecatSyncAll      bool
	polecatSyncFromMain bool
	polecatStatusJSON   bool
)

func init() {
	// List flags
	polecatListCmd.Flags().BoolVar(&polecatListJSON, "json", false, "Output as JSON")
	polecatListCmd.Flags().BoolVar(&polecatListAll, "all", false, "List polecats in all rigs")

	// Remove flags
	polecatRemoveCmd.Flags().BoolVarP(&polecatForce, "force", "f", false, "Force removal, bypassing checks")
	polecatRemoveCmd.Flags().BoolVar(&polecatRemoveAll, "all", false, "Remove all polecats in the rig")

	// Sync flags
	polecatSyncCmd.Flags().BoolVar(&polecatSyncAll, "all", false, "Sync all polecats in the rig")
	polecatSyncCmd.Flags().BoolVar(&polecatSyncFromMain, "from-main", false, "Pull only, no push")

	// Status flags
	polecatStatusCmd.Flags().BoolVar(&polecatStatusJSON, "json", false, "Output as JSON")

	// Add subcommands
	polecatCmd.AddCommand(polecatListCmd)
	polecatCmd.AddCommand(polecatAddCmd)
	polecatCmd.AddCommand(polecatRemoveCmd)
	polecatCmd.AddCommand(polecatWakeCmd)
	polecatCmd.AddCommand(polecatSleepCmd)
	polecatCmd.AddCommand(polecatDoneCmd)
	polecatCmd.AddCommand(polecatResetCmd)
	polecatCmd.AddCommand(polecatSyncCmd)
	polecatCmd.AddCommand(polecatStatusCmd)

	rootCmd.AddCommand(polecatCmd)
}

// PolecatListItem represents a polecat in list output.
type PolecatListItem struct {
	Rig            string        `json:"rig"`
	Name           string        `json:"name"`
	State          polecat.State `json:"state"`
	Issue          string        `json:"issue,omitempty"`
	SessionRunning bool          `json:"session_running"`
}

// getPolecatManager creates a polecat manager for the given rig.
func getPolecatManager(rigName string) (*polecat.Manager, *rig.Rig, error) {
	_, r, err := getRig(rigName)
	if err != nil {
		return nil, nil, err
	}

	polecatGit := git.NewGit(r.Path)
	mgr := polecat.NewManager(r, polecatGit)

	return mgr, r, nil
}

func runPolecatList(cmd *cobra.Command, args []string) error {
	var rigs []*rig.Rig

	if polecatListAll {
		// List all rigs
		allRigs, _, err := getAllRigs()
		if err != nil {
			return err
		}
		rigs = allRigs
	} else {
		// Need a rig name
		if len(args) < 1 {
			return fmt.Errorf("rig name required (or use --all)")
		}
		_, r, err := getPolecatManager(args[0])
		if err != nil {
			return err
		}
		rigs = []*rig.Rig{r}
	}

	// Collect polecats from all rigs
	t := tmux.NewTmux()
	var allPolecats []PolecatListItem

	for _, r := range rigs {
		polecatGit := git.NewGit(r.Path)
		mgr := polecat.NewManager(r, polecatGit)
		sessMgr := session.NewManager(t, r)

		polecats, err := mgr.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to list polecats in %s: %v\n", r.Name, err)
			continue
		}

		for _, p := range polecats {
			running, _ := sessMgr.IsRunning(p.Name)
			allPolecats = append(allPolecats, PolecatListItem{
				Rig:            r.Name,
				Name:           p.Name,
				State:          p.State,
				Issue:          p.Issue,
				SessionRunning: running,
			})
		}
	}

	// Output
	if polecatListJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(allPolecats)
	}

	if len(allPolecats) == 0 {
		fmt.Println("No active polecats found.")
		return nil
	}

	fmt.Printf("%s\n\n", style.Bold.Render("Active Polecats"))
	for _, p := range allPolecats {
		// Session indicator
		sessionStatus := style.Dim.Render("○")
		if p.SessionRunning {
			sessionStatus = style.Success.Render("●")
		}

		// Normalize state for display (legacy idle/active → working)
		displayState := p.State
		if p.State == polecat.StateIdle || p.State == polecat.StateActive {
			displayState = polecat.StateWorking
		}

		// State color
		stateStr := string(displayState)
		switch displayState {
		case polecat.StateWorking:
			stateStr = style.Info.Render(stateStr)
		case polecat.StateStuck:
			stateStr = style.Warning.Render(stateStr)
		case polecat.StateDone:
			stateStr = style.Success.Render(stateStr)
		default:
			stateStr = style.Dim.Render(stateStr)
		}

		fmt.Printf("  %s %s/%s  %s\n", sessionStatus, p.Rig, p.Name, stateStr)
		if p.Issue != "" {
			fmt.Printf("    %s\n", style.Dim.Render(p.Issue))
		}
	}

	return nil
}

func runPolecatAdd(cmd *cobra.Command, args []string) error {
	rigName := args[0]
	polecatName := args[1]

	mgr, _, err := getPolecatManager(rigName)
	if err != nil {
		return err
	}

	fmt.Printf("Adding polecat %s to rig %s...\n", polecatName, rigName)

	p, err := mgr.Add(polecatName)
	if err != nil {
		return fmt.Errorf("adding polecat: %w", err)
	}

	fmt.Printf("%s Polecat %s added.\n", style.SuccessPrefix, p.Name)
	fmt.Printf("  %s\n", style.Dim.Render(p.ClonePath))
	fmt.Printf("  Branch: %s\n", style.Dim.Render(p.Branch))

	return nil
}

func runPolecatRemove(cmd *cobra.Command, args []string) error {
	// Build list of polecats to remove
	type polecatToRemove struct {
		rigName     string
		polecatName string
		mgr         *polecat.Manager
		r           *rig.Rig
	}
	var toRemove []polecatToRemove

	if polecatRemoveAll {
		// --all flag: first arg is just the rig name
		rigName := args[0]
		// Check if it looks like rig/polecat format
		if _, _, err := parseAddress(rigName); err == nil {
			return fmt.Errorf("with --all, provide just the rig name (e.g., 'gt polecat remove gastown --all')")
		}

		mgr, r, err := getPolecatManager(rigName)
		if err != nil {
			return err
		}

		polecats, err := mgr.List()
		if err != nil {
			return fmt.Errorf("listing polecats: %w", err)
		}

		if len(polecats) == 0 {
			fmt.Println("No polecats to remove.")
			return nil
		}

		for _, p := range polecats {
			toRemove = append(toRemove, polecatToRemove{
				rigName:     rigName,
				polecatName: p.Name,
				mgr:         mgr,
				r:           r,
			})
		}
	} else {
		// Multiple rig/polecat arguments
		for _, arg := range args {
			rigName, polecatName, err := parseAddress(arg)
			if err != nil {
				return fmt.Errorf("invalid address '%s': %w", arg, err)
			}

			mgr, r, err := getPolecatManager(rigName)
			if err != nil {
				return err
			}

			toRemove = append(toRemove, polecatToRemove{
				rigName:     rigName,
				polecatName: polecatName,
				mgr:         mgr,
				r:           r,
			})
		}
	}

	// Remove each polecat
	t := tmux.NewTmux()
	var removeErrors []string
	removed := 0

	for _, p := range toRemove {
		// Check if session is running
		if !polecatForce {
			sessMgr := session.NewManager(t, p.r)
			running, _ := sessMgr.IsRunning(p.polecatName)
			if running {
				removeErrors = append(removeErrors, fmt.Sprintf("%s/%s: session is running (stop first or use --force)", p.rigName, p.polecatName))
				continue
			}
		}

		fmt.Printf("Removing polecat %s/%s...\n", p.rigName, p.polecatName)

		if err := p.mgr.Remove(p.polecatName, polecatForce); err != nil {
			if errors.Is(err, polecat.ErrHasChanges) {
				removeErrors = append(removeErrors, fmt.Sprintf("%s/%s: has uncommitted changes (use --force)", p.rigName, p.polecatName))
			} else {
				removeErrors = append(removeErrors, fmt.Sprintf("%s/%s: %v", p.rigName, p.polecatName, err))
			}
			continue
		}

		fmt.Printf("  %s removed\n", style.Success.Render("✓"))
		removed++
	}

	// Report results
	if len(removeErrors) > 0 {
		fmt.Printf("\n%s Some removals failed:\n", style.Warning.Render("Warning:"))
		for _, e := range removeErrors {
			fmt.Printf("  - %s\n", e)
		}
	}

	if removed > 0 {
		fmt.Printf("\n%s Removed %d polecat(s).\n", style.SuccessPrefix, removed)
	}

	if len(removeErrors) > 0 {
		return fmt.Errorf("%d removal(s) failed", len(removeErrors))
	}

	return nil
}

func runPolecatWake(cmd *cobra.Command, args []string) error {
	fmt.Println(style.Warning.Render("DEPRECATED: Use 'gt spawn' to create fresh polecats instead"))
	fmt.Println()

	rigName, polecatName, err := parseAddress(args[0])
	if err != nil {
		return err
	}

	mgr, _, err := getPolecatManager(rigName)
	if err != nil {
		return err
	}

	if err := mgr.Wake(polecatName); err != nil {
		return fmt.Errorf("waking polecat: %w", err)
	}

	fmt.Printf("%s Polecat %s is now working.\n", style.SuccessPrefix, polecatName)
	return nil
}

func runPolecatSleep(cmd *cobra.Command, args []string) error {
	fmt.Println(style.Warning.Render("DEPRECATED: Use 'gt handoff' from within a polecat session instead"))
	fmt.Println()

	rigName, polecatName, err := parseAddress(args[0])
	if err != nil {
		return err
	}

	mgr, r, err := getPolecatManager(rigName)
	if err != nil {
		return err
	}

	// Check if session is running
	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, r)
	running, _ := sessMgr.IsRunning(polecatName)
	if running {
		return fmt.Errorf("session is running. Use 'gt handoff' from the polecat session, or stop it with: gt session stop %s/%s", rigName, polecatName)
	}

	if err := mgr.Sleep(polecatName); err != nil {
		return fmt.Errorf("marking polecat as done: %w", err)
	}

	fmt.Printf("%s Polecat %s is now done.\n", style.SuccessPrefix, polecatName)
	return nil
}

func runPolecatDone(cmd *cobra.Command, args []string) error {
	rigName, polecatName, err := parseAddress(args[0])
	if err != nil {
		return err
	}

	mgr, r, err := getPolecatManager(rigName)
	if err != nil {
		return err
	}

	// Check if session is running
	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, r)
	running, _ := sessMgr.IsRunning(polecatName)
	if running {
		return fmt.Errorf("session is running. Stop it first with: gt session stop %s/%s", rigName, polecatName)
	}

	if err := mgr.Finish(polecatName); err != nil {
		return fmt.Errorf("finishing polecat: %w", err)
	}

	fmt.Printf("%s Polecat %s is now idle.\n", style.SuccessPrefix, polecatName)
	return nil
}

func runPolecatReset(cmd *cobra.Command, args []string) error {
	rigName, polecatName, err := parseAddress(args[0])
	if err != nil {
		return err
	}

	mgr, r, err := getPolecatManager(rigName)
	if err != nil {
		return err
	}

	// Check if session is running
	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, r)
	running, _ := sessMgr.IsRunning(polecatName)
	if running {
		return fmt.Errorf("session is running. Stop it first with: gt session stop %s/%s", rigName, polecatName)
	}

	if err := mgr.Reset(polecatName); err != nil {
		return fmt.Errorf("resetting polecat: %w", err)
	}

	fmt.Printf("%s Polecat %s has been reset to idle.\n", style.SuccessPrefix, polecatName)
	return nil
}

func runPolecatSync(cmd *cobra.Command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("rig or rig/polecat address required")
	}

	// Parse address - could be "rig" or "rig/polecat"
	rigName, polecatName, err := parseAddress(args[0])
	if err != nil {
		// Might just be a rig name
		rigName = args[0]
		polecatName = ""
	}

	mgr, r, err := getPolecatManager(rigName)
	if err != nil {
		return err
	}

	// Get list of polecats to sync
	var polecatsToSync []string
	if polecatSyncAll || polecatName == "" {
		polecats, err := mgr.List()
		if err != nil {
			return fmt.Errorf("listing polecats: %w", err)
		}
		for _, p := range polecats {
			polecatsToSync = append(polecatsToSync, p.Name)
		}
	} else {
		polecatsToSync = []string{polecatName}
	}

	if len(polecatsToSync) == 0 {
		fmt.Println("No polecats to sync.")
		return nil
	}

	// Sync each polecat
	var syncErrors []string
	for _, name := range polecatsToSync {
		polecatDir := filepath.Join(r.Path, "polecats", name)

		// Check directory exists
		if _, err := os.Stat(polecatDir); os.IsNotExist(err) {
			syncErrors = append(syncErrors, fmt.Sprintf("%s: directory not found", name))
			continue
		}

		// Build sync command
		syncArgs := []string{"sync"}
		if polecatSyncFromMain {
			syncArgs = append(syncArgs, "--from-main")
		}

		fmt.Printf("Syncing %s/%s...\n", rigName, name)

		syncCmd := exec.Command("bd", syncArgs...)
		syncCmd.Dir = polecatDir
		output, err := syncCmd.CombinedOutput()
		if err != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("%s: %v", name, err))
			if len(output) > 0 {
				fmt.Printf("  %s\n", style.Dim.Render(string(output)))
			}
		} else {
			fmt.Printf("  %s\n", style.Success.Render("✓ synced"))
		}
	}

	if len(syncErrors) > 0 {
		fmt.Printf("\n%s Some syncs failed:\n", style.Warning.Render("Warning:"))
		for _, e := range syncErrors {
			fmt.Printf("  - %s\n", e)
		}
		return fmt.Errorf("%d sync(s) failed", len(syncErrors))
	}

	return nil
}

// PolecatStatus represents detailed polecat status for JSON output.
type PolecatStatus struct {
	Rig            string        `json:"rig"`
	Name           string        `json:"name"`
	State          polecat.State `json:"state"`
	Issue          string        `json:"issue,omitempty"`
	ClonePath      string        `json:"clone_path"`
	Branch         string        `json:"branch"`
	SessionRunning bool          `json:"session_running"`
	SessionID      string        `json:"session_id,omitempty"`
	Attached       bool          `json:"attached,omitempty"`
	Windows        int           `json:"windows,omitempty"`
	CreatedAt      string        `json:"created_at,omitempty"`
	LastActivity   string        `json:"last_activity,omitempty"`
}

func runPolecatStatus(cmd *cobra.Command, args []string) error {
	rigName, polecatName, err := parseAddress(args[0])
	if err != nil {
		return err
	}

	mgr, r, err := getPolecatManager(rigName)
	if err != nil {
		return err
	}

	// Get polecat info
	p, err := mgr.Get(polecatName)
	if err != nil {
		return fmt.Errorf("polecat '%s' not found in rig '%s'", polecatName, rigName)
	}

	// Get session info
	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, r)
	sessInfo, err := sessMgr.Status(polecatName)
	if err != nil {
		// Non-fatal - continue without session info
		sessInfo = &session.Info{
			Polecat: polecatName,
			Running: false,
		}
	}

	// JSON output
	if polecatStatusJSON {
		status := PolecatStatus{
			Rig:            rigName,
			Name:           polecatName,
			State:          p.State,
			Issue:          p.Issue,
			ClonePath:      p.ClonePath,
			Branch:         p.Branch,
			SessionRunning: sessInfo.Running,
			SessionID:      sessInfo.SessionID,
			Attached:       sessInfo.Attached,
			Windows:        sessInfo.Windows,
		}
		if !sessInfo.Created.IsZero() {
			status.CreatedAt = sessInfo.Created.Format("2006-01-02 15:04:05")
		}
		if !sessInfo.LastActivity.IsZero() {
			status.LastActivity = sessInfo.LastActivity.Format("2006-01-02 15:04:05")
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	}

	// Human-readable output
	fmt.Printf("%s\n\n", style.Bold.Render(fmt.Sprintf("Polecat: %s/%s", rigName, polecatName)))

	// State with color
	stateStr := string(p.State)
	switch p.State {
	case polecat.StateWorking:
		stateStr = style.Info.Render(stateStr)
	case polecat.StateStuck:
		stateStr = style.Warning.Render(stateStr)
	case polecat.StateDone:
		stateStr = style.Success.Render(stateStr)
	default:
		stateStr = style.Dim.Render(stateStr)
	}
	fmt.Printf("  State:         %s\n", stateStr)

	// Issue
	if p.Issue != "" {
		fmt.Printf("  Issue:         %s\n", p.Issue)
	} else {
		fmt.Printf("  Issue:         %s\n", style.Dim.Render("(none)"))
	}

	// Clone path and branch
	fmt.Printf("  Clone:         %s\n", style.Dim.Render(p.ClonePath))
	fmt.Printf("  Branch:        %s\n", style.Dim.Render(p.Branch))

	// Session info
	fmt.Println()
	fmt.Printf("%s\n", style.Bold.Render("Session"))

	if sessInfo.Running {
		fmt.Printf("  Status:        %s\n", style.Success.Render("running"))
		fmt.Printf("  Session ID:    %s\n", style.Dim.Render(sessInfo.SessionID))

		if sessInfo.Attached {
			fmt.Printf("  Attached:      %s\n", style.Info.Render("yes"))
		} else {
			fmt.Printf("  Attached:      %s\n", style.Dim.Render("no"))
		}

		if sessInfo.Windows > 0 {
			fmt.Printf("  Windows:       %d\n", sessInfo.Windows)
		}

		if !sessInfo.Created.IsZero() {
			fmt.Printf("  Created:       %s\n", sessInfo.Created.Format("2006-01-02 15:04:05"))
		}

		if !sessInfo.LastActivity.IsZero() {
			// Show relative time for activity
			ago := formatActivityTime(sessInfo.LastActivity)
			fmt.Printf("  Last Activity: %s (%s)\n",
				sessInfo.LastActivity.Format("15:04:05"),
				style.Dim.Render(ago))
		}
	} else {
		fmt.Printf("  Status:        %s\n", style.Dim.Render("not running"))
	}

	return nil
}

// formatActivityTime returns a human-readable relative time string.
func formatActivityTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d seconds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
