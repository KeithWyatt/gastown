package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	shutdownGraceful     bool
	shutdownWait         int
	shutdownAll          bool
	shutdownYes          bool
	shutdownPolecatsOnly bool
	shutdownNuclear      bool
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start Gas Town",
	Long: `Start Gas Town by launching the Deacon and Mayor.

The Deacon is the health-check orchestrator that monitors Mayor and Witnesses.
The Mayor is the global coordinator that dispatches work.

Other agents (Witnesses, Refineries, Polecats) are started lazily as needed.

To stop Gas Town, use 'gt shutdown'.`,
	RunE: runStart,
}

var shutdownCmd = &cobra.Command{
	Use:   "shutdown",
	Short: "Shutdown Gas Town",
	Long: `Shutdown Gas Town by stopping agents and cleaning up polecats.

By default, preserves crew sessions (your persistent workspaces).
Prompts for confirmation before stopping.

After killing sessions, polecats are cleaned up:
  - Worktrees are removed
  - Polecat branches are deleted
  - Polecats with uncommitted work are SKIPPED (protected)

Shutdown levels (progressively more aggressive):
  (default)       - Stop infrastructure (Mayor, Deacon, Witnesses, Refineries, Polecats)
  --all           - Also stop crew sessions
  --polecats-only - Only stop polecats (leaves everything else running)

Use --graceful to allow agents time to save state before killing.
Use --yes to skip confirmation prompt.
Use --nuclear to force cleanup even if polecats have uncommitted work (DANGER).`,
	RunE: runShutdown,
}

func init() {
	shutdownCmd.Flags().BoolVarP(&shutdownGraceful, "graceful", "g", false,
		"Send ESC to agents and wait for them to handoff before killing")
	shutdownCmd.Flags().IntVarP(&shutdownWait, "wait", "w", 30,
		"Seconds to wait for graceful shutdown (default 30)")
	shutdownCmd.Flags().BoolVarP(&shutdownAll, "all", "a", false,
		"Also stop crew sessions (by default, crew is preserved)")
	shutdownCmd.Flags().BoolVarP(&shutdownYes, "yes", "y", false,
		"Skip confirmation prompt")
	shutdownCmd.Flags().BoolVar(&shutdownPolecatsOnly, "polecats-only", false,
		"Only stop polecats (minimal shutdown)")
	shutdownCmd.Flags().BoolVar(&shutdownNuclear, "nuclear", false,
		"Force cleanup even if polecats have uncommitted work (DANGER: may lose work)")

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(shutdownCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	// Verify we're in a Gas Town workspace
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	t := tmux.NewTmux()

	fmt.Printf("Starting Gas Town from %s\n\n", style.Dim.Render(townRoot))

	// Start Mayor first (so Deacon sees it as up)
	mayorRunning, _ := t.HasSession(MayorSessionName)
	if mayorRunning {
		fmt.Printf("  %s Mayor already running\n", style.Dim.Render("○"))
	} else {
		fmt.Printf("  %s Starting Mayor...\n", style.Bold.Render("→"))
		if err := startMayorSession(t); err != nil {
			return fmt.Errorf("starting Mayor: %w", err)
		}
		fmt.Printf("  %s Mayor started\n", style.Bold.Render("✓"))
	}

	// Start Deacon (health monitor)
	deaconRunning, _ := t.HasSession(DeaconSessionName)
	if deaconRunning {
		fmt.Printf("  %s Deacon already running\n", style.Dim.Render("○"))
	} else {
		fmt.Printf("  %s Starting Deacon...\n", style.Bold.Render("→"))
		if err := startDeaconSession(t); err != nil {
			return fmt.Errorf("starting Deacon: %w", err)
		}
		fmt.Printf("  %s Deacon started\n", style.Bold.Render("✓"))
	}

	fmt.Println()
	fmt.Printf("%s Gas Town is running\n", style.Bold.Render("✓"))
	fmt.Println()
	fmt.Printf("  Attach to Mayor:  %s\n", style.Dim.Render("gt mayor attach"))
	fmt.Printf("  Attach to Deacon: %s\n", style.Dim.Render("gt deacon attach"))
	fmt.Printf("  Check status:     %s\n", style.Dim.Render("gt status"))

	return nil
}

func runShutdown(cmd *cobra.Command, args []string) error {
	t := tmux.NewTmux()

	// Find workspace root for polecat cleanup
	townRoot, _ := workspace.FindFromCwd()

	// Collect sessions to show what will be stopped
	sessions, err := t.ListSessions()
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	toStop, preserved := categorizeSessions(sessions)

	if len(toStop) == 0 {
		fmt.Printf("%s Gas Town was not running\n", style.Dim.Render("○"))
		return nil
	}

	// Show what will happen
	fmt.Println("Sessions to stop:")
	for _, sess := range toStop {
		fmt.Printf("  %s %s\n", style.Bold.Render("→"), sess)
	}
	if len(preserved) > 0 && !shutdownAll {
		fmt.Println()
		fmt.Println("Sessions preserved (crew):")
		for _, sess := range preserved {
			fmt.Printf("  %s %s\n", style.Dim.Render("○"), sess)
		}
	}
	fmt.Println()

	// Confirmation prompt
	if !shutdownYes {
		fmt.Printf("Proceed with shutdown? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Shutdown cancelled.")
			return nil
		}
	}

	if shutdownGraceful {
		return runGracefulShutdown(t, toStop, townRoot)
	}
	return runImmediateShutdown(t, toStop, townRoot)
}

// categorizeSessions splits sessions into those to stop and those to preserve.
func categorizeSessions(sessions []string) (toStop, preserved []string) {
	for _, sess := range sessions {
		if !strings.HasPrefix(sess, "gt-") {
			continue // Not a Gas Town session
		}

		// Check if it's a crew session (pattern: gt-<rig>-crew-<name>)
		isCrew := strings.Contains(sess, "-crew-")

		// Check if it's a polecat session (pattern: gt-<rig>-<name> where name is not crew/witness/refinery)
		isPolecat := false
		if !isCrew && sess != MayorSessionName && sess != DeaconSessionName {
			parts := strings.Split(sess, "-")
			if len(parts) >= 3 {
				role := parts[2]
				if role != "witness" && role != "refinery" && role != "crew" {
					isPolecat = true
				}
			}
		}

		// Decide based on flags
		if shutdownPolecatsOnly {
			// Only stop polecats
			if isPolecat {
				toStop = append(toStop, sess)
			} else {
				preserved = append(preserved, sess)
			}
		} else if shutdownAll {
			// Stop everything
			toStop = append(toStop, sess)
		} else {
			// Default: preserve crew
			if isCrew {
				preserved = append(preserved, sess)
			} else {
				toStop = append(toStop, sess)
			}
		}
	}
	return
}

func runGracefulShutdown(t *tmux.Tmux, gtSessions []string, townRoot string) error {
	fmt.Printf("Graceful shutdown of Gas Town (waiting up to %ds)...\n\n", shutdownWait)

	// Phase 1: Send ESC to all agents to interrupt them
	fmt.Printf("Phase 1: Sending ESC to %d agent(s)...\n", len(gtSessions))
	for _, sess := range gtSessions {
		fmt.Printf("  %s Interrupting %s\n", style.Bold.Render("→"), sess)
		_ = t.SendKeysRaw(sess, "Escape")
	}

	// Phase 2: Send shutdown message asking agents to handoff
	fmt.Printf("\nPhase 2: Requesting handoff from agents...\n")
	shutdownMsg := "[SHUTDOWN] Gas Town is shutting down. Please save your state and update your handoff bead, then type /exit or wait to be terminated."
	for _, sess := range gtSessions {
		// Small delay then send the message
		time.Sleep(500 * time.Millisecond)
		_ = t.SendKeys(sess, shutdownMsg)
	}

	// Phase 3: Wait for agents to complete handoff
	fmt.Printf("\nPhase 3: Waiting %ds for agents to complete handoff...\n", shutdownWait)
	fmt.Printf("  %s\n", style.Dim.Render("(Press Ctrl-C to force immediate shutdown)"))

	// Wait with countdown
	for remaining := shutdownWait; remaining > 0; remaining -= 5 {
		if remaining < shutdownWait {
			fmt.Printf("  %s %ds remaining...\n", style.Dim.Render("⏳"), remaining)
		}
		sleepTime := 5
		if remaining < 5 {
			sleepTime = remaining
		}
		time.Sleep(time.Duration(sleepTime) * time.Second)
	}

	// Phase 4: Kill sessions in correct order
	fmt.Printf("\nPhase 4: Terminating sessions...\n")
	stopped := killSessionsInOrder(t, gtSessions)

	// Phase 5: Cleanup polecat worktrees and branches
	fmt.Printf("\nPhase 5: Cleaning up polecats...\n")
	if townRoot != "" {
		cleanupPolecats(townRoot)
	}

	fmt.Println()
	fmt.Printf("%s Graceful shutdown complete (%d sessions stopped)\n", style.Bold.Render("✓"), stopped)
	return nil
}

func runImmediateShutdown(t *tmux.Tmux, gtSessions []string, townRoot string) error {
	fmt.Println("Shutting down Gas Town...")

	stopped := killSessionsInOrder(t, gtSessions)

	// Cleanup polecat worktrees and branches
	if townRoot != "" {
		fmt.Println()
		fmt.Println("Cleaning up polecats...")
		cleanupPolecats(townRoot)
	}

	fmt.Println()
	fmt.Printf("%s Gas Town shutdown complete (%d sessions stopped)\n", style.Bold.Render("✓"), stopped)

	return nil
}

// killSessionsInOrder stops sessions in the correct order:
// 1. Deacon first (so it doesn't restart others)
// 2. Everything except Mayor
// 3. Mayor last
func killSessionsInOrder(t *tmux.Tmux, sessions []string) int {
	stopped := 0

	// Helper to check if session is in our list
	inList := func(sess string) bool {
		for _, s := range sessions {
			if s == sess {
				return true
			}
		}
		return false
	}

	// 1. Stop Deacon first
	if inList(DeaconSessionName) {
		if err := t.KillSession(DeaconSessionName); err == nil {
			fmt.Printf("  %s %s stopped\n", style.Bold.Render("✓"), DeaconSessionName)
			stopped++
		}
	}

	// 2. Stop others (except Mayor)
	for _, sess := range sessions {
		if sess == DeaconSessionName || sess == MayorSessionName {
			continue
		}
		if err := t.KillSession(sess); err == nil {
			fmt.Printf("  %s %s stopped\n", style.Bold.Render("✓"), sess)
			stopped++
		}
	}

	// 3. Stop Mayor last
	if inList(MayorSessionName) {
		if err := t.KillSession(MayorSessionName); err == nil {
			fmt.Printf("  %s %s stopped\n", style.Bold.Render("✓"), MayorSessionName)
			stopped++
		}
	}

	return stopped
}

// cleanupPolecats removes polecat worktrees and branches for all rigs.
// It refuses to clean up polecats with uncommitted work unless --nuclear is set.
func cleanupPolecats(townRoot string) {
	// Load rigs config
	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		fmt.Printf("  %s Could not load rigs config: %v\n", style.Dim.Render("○"), err)
		return
	}

	g := git.NewGit(townRoot)
	rigMgr := rig.NewManager(townRoot, rigsConfig, g)

	// Discover all rigs
	rigs, err := rigMgr.DiscoverRigs()
	if err != nil {
		fmt.Printf("  %s Could not discover rigs: %v\n", style.Dim.Render("○"), err)
		return
	}

	totalCleaned := 0
	totalSkipped := 0
	var uncommittedPolecats []string

	for _, r := range rigs {
		polecatGit := git.NewGit(r.Path)
		polecatMgr := polecat.NewManager(r, polecatGit)

		polecats, err := polecatMgr.List()
		if err != nil {
			continue
		}

		for _, p := range polecats {
			// Check for uncommitted work
			pGit := git.NewGit(p.ClonePath)
			status, err := pGit.CheckUncommittedWork()
			if err != nil {
				// Can't check, be safe and skip unless nuclear
				if !shutdownNuclear {
					fmt.Printf("  %s %s/%s: could not check status, skipping\n",
						style.Dim.Render("○"), r.Name, p.Name)
					totalSkipped++
					continue
				}
			} else if !status.Clean() {
				// Has uncommitted work
				if !shutdownNuclear {
					uncommittedPolecats = append(uncommittedPolecats,
						fmt.Sprintf("%s/%s (%s)", r.Name, p.Name, status.String()))
					totalSkipped++
					continue
				}
				// Nuclear mode: warn but proceed
				fmt.Printf("  %s %s/%s: NUCLEAR - removing despite %s\n",
					style.Bold.Render("⚠"), r.Name, p.Name, status.String())
			}

			// Clean: remove worktree and branch
			if err := polecatMgr.RemoveWithOptions(p.Name, true, shutdownNuclear); err != nil {
				fmt.Printf("  %s %s/%s: cleanup failed: %v\n",
					style.Dim.Render("○"), r.Name, p.Name, err)
				totalSkipped++
				continue
			}

			// Delete the polecat branch from mayor's clone
			branchName := fmt.Sprintf("polecat/%s", p.Name)
			mayorPath := filepath.Join(r.Path, "mayor", "rig")
			mayorGit := git.NewGit(mayorPath)
			_ = mayorGit.DeleteBranch(branchName, true) // Ignore errors

			fmt.Printf("  %s %s/%s: cleaned up\n", style.Bold.Render("✓"), r.Name, p.Name)
			totalCleaned++
		}
	}

	// Summary
	if len(uncommittedPolecats) > 0 {
		fmt.Println()
		fmt.Printf("  %s Polecats with uncommitted work (use --nuclear to force):\n",
			style.Bold.Render("⚠"))
		for _, pc := range uncommittedPolecats {
			fmt.Printf("    • %s\n", pc)
		}
	}

	if totalCleaned > 0 || totalSkipped > 0 {
		fmt.Printf("  Cleaned: %d, Skipped: %d\n", totalCleaned, totalSkipped)
	} else {
		fmt.Printf("  %s No polecats to clean up\n", style.Dim.Render("○"))
	}
}
