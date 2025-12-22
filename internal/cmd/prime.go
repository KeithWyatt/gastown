package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/lock"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/templates"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Role represents a detected agent role.
type Role string

const (
	RoleMayor    Role = "mayor"
	RoleDeacon   Role = "deacon"
	RoleWitness  Role = "witness"
	RoleRefinery Role = "refinery"
	RolePolecat  Role = "polecat"
	RoleCrew     Role = "crew"
	RoleUnknown  Role = "unknown"
)

var primeCmd = &cobra.Command{
	Use:   "prime",
	Short: "Output role context for current directory",
	Long: `Detect the agent role from the current directory and output context.

Role detection:
  - Town root or mayor/rig/ → Mayor context
  - <rig>/witness/rig/ → Witness context
  - <rig>/refinery/rig/ → Refinery context
  - <rig>/polecats/<name>/ → Polecat context

This command is typically used in shell prompts or agent initialization.`,
	RunE: runPrime,
}

func init() {
	rootCmd.AddCommand(primeCmd)
}

// RoleContext contains information about the detected role.
type RoleContext struct {
	Role     Role   `json:"role"`
	Rig      string `json:"rig,omitempty"`
	Polecat  string `json:"polecat,omitempty"`
	TownRoot string `json:"town_root"`
	WorkDir  string `json:"work_dir"`
}

func runPrime(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}

	// Find town root
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding workspace: %w", err)
	}
	if townRoot == "" {
		return fmt.Errorf("not in a Gas Town workspace")
	}

	// Detect role
	ctx := detectRole(cwd, townRoot)

	// Check and acquire identity lock for worker roles
	if err := acquireIdentityLock(ctx); err != nil {
		return err
	}

	// Ensure beads redirect exists for worktree-based roles
	ensureBeadsRedirect(ctx)

	// Output context
	if err := outputPrimeContext(ctx); err != nil {
		return err
	}

	// Output handoff content if present
	outputHandoffContent(ctx)

	// Output molecule context if working on a molecule step
	outputMoleculeContext(ctx)

	// Run bd prime to output beads workflow context
	runBdPrime(cwd)

	// Run gt mail check --inject to inject any pending mail
	runMailCheckInject(cwd)

	// Output startup directive for roles that should announce themselves
	outputStartupDirective(ctx)

	return nil
}

func detectRole(cwd, townRoot string) RoleContext {
	ctx := RoleContext{
		Role:     RoleUnknown,
		TownRoot: townRoot,
		WorkDir:  cwd,
	}

	// Get relative path from town root
	relPath, err := filepath.Rel(townRoot, cwd)
	if err != nil {
		return ctx
	}

	// Normalize and split path
	relPath = filepath.ToSlash(relPath)
	parts := strings.Split(relPath, "/")

	// Check for mayor role
	// At town root, or in mayor/ or mayor/rig/
	if relPath == "." || relPath == "" {
		ctx.Role = RoleMayor
		return ctx
	}
	if len(parts) >= 1 && parts[0] == "mayor" {
		ctx.Role = RoleMayor
		return ctx
	}

	// Check for deacon role: deacon/
	if len(parts) >= 1 && parts[0] == "deacon" {
		ctx.Role = RoleDeacon
		return ctx
	}

	// At this point, first part should be a rig name
	if len(parts) < 1 {
		return ctx
	}
	rigName := parts[0]
	ctx.Rig = rigName

	// Check for witness: <rig>/witness/rig/
	if len(parts) >= 2 && parts[1] == "witness" {
		ctx.Role = RoleWitness
		return ctx
	}

	// Check for refinery: <rig>/refinery/rig/
	if len(parts) >= 2 && parts[1] == "refinery" {
		ctx.Role = RoleRefinery
		return ctx
	}

	// Check for polecat: <rig>/polecats/<name>/
	if len(parts) >= 3 && parts[1] == "polecats" {
		ctx.Role = RolePolecat
		ctx.Polecat = parts[2]
		return ctx
	}

	// Check for crew: <rig>/crew/<name>/
	if len(parts) >= 3 && parts[1] == "crew" {
		ctx.Role = RoleCrew
		ctx.Polecat = parts[2] // Use Polecat field for crew member name
		return ctx
	}

	// Default: could be rig root - treat as unknown
	return ctx
}

func outputPrimeContext(ctx RoleContext) error {
	// Try to use templates first
	tmpl, err := templates.New()
	if err != nil {
		// Fall back to hardcoded output if templates fail
		return outputPrimeContextFallback(ctx)
	}

	// Map role to template name
	var roleName string
	switch ctx.Role {
	case RoleMayor:
		roleName = "mayor"
	case RoleDeacon:
		roleName = "deacon"
	case RoleWitness:
		roleName = "witness"
	case RoleRefinery:
		roleName = "refinery"
	case RolePolecat:
		roleName = "polecat"
	case RoleCrew:
		roleName = "crew"
	default:
		// Unknown role - use fallback
		return outputPrimeContextFallback(ctx)
	}

	// Build template data
	data := templates.RoleData{
		Role:     roleName,
		RigName:  ctx.Rig,
		TownRoot: ctx.TownRoot,
		WorkDir:  ctx.WorkDir,
		Polecat:  ctx.Polecat,
	}

	// Render and output
	output, err := tmpl.RenderRole(roleName, data)
	if err != nil {
		return fmt.Errorf("rendering template: %w", err)
	}

	fmt.Print(output)
	return nil
}

func outputPrimeContextFallback(ctx RoleContext) error {
	switch ctx.Role {
	case RoleMayor:
		outputMayorContext(ctx)
	case RoleWitness:
		outputWitnessContext(ctx)
	case RoleRefinery:
		outputRefineryContext(ctx)
	case RolePolecat:
		outputPolecatContext(ctx)
	case RoleCrew:
		outputCrewContext(ctx)
	default:
		outputUnknownContext(ctx)
	}
	return nil
}

func outputMayorContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Mayor Context"))
	fmt.Println("You are the **Mayor** - the global coordinator of Gas Town.")
	fmt.Println()
	fmt.Println("## Responsibilities")
	fmt.Println("- Coordinate work across all rigs")
	fmt.Println("- Delegate to Refineries, not directly to polecats")
	fmt.Println("- Monitor overall system health")
	fmt.Println()
	fmt.Println("## Key Commands")
	fmt.Println("- `gt mail inbox` - Check your messages")
	fmt.Println("- `gt mail read <id>` - Read a specific message")
	fmt.Println("- `gt status` - Show overall town status")
	fmt.Println("- `gt rigs` - List all rigs")
	fmt.Println("- `bd ready` - Issues ready to work")
	fmt.Println()
	fmt.Println("## Startup")
	fmt.Println("Check for handoff messages with 🤝 HANDOFF in subject - continue predecessor's work.")
	fmt.Println()
	fmt.Printf("Town root: %s\n", style.Dim.Render(ctx.TownRoot))
}

func outputWitnessContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Witness Context"))
	fmt.Printf("You are the **Witness** for rig: %s\n\n", style.Bold.Render(ctx.Rig))
	fmt.Println("## Responsibilities")
	fmt.Println("- Monitor polecat health via heartbeat")
	fmt.Println("- Spawn replacement agents for stuck polecats")
	fmt.Println("- Report rig status to Mayor")
	fmt.Println()
	fmt.Println("## Key Commands")
	fmt.Println("- `gt witness status` - Show witness status")
	fmt.Println("- `gt polecats` - List polecats in this rig")
	fmt.Println()
	fmt.Printf("Rig: %s\n", style.Dim.Render(ctx.Rig))
}

func outputRefineryContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Refinery Context"))
	fmt.Printf("You are the **Refinery** for rig: %s\n\n", style.Bold.Render(ctx.Rig))
	fmt.Println("## Responsibilities")
	fmt.Println("- Process the merge queue for this rig")
	fmt.Println("- Merge polecat work to integration branch")
	fmt.Println("- Resolve merge conflicts")
	fmt.Println("- Land completed swarms to main")
	fmt.Println()
	fmt.Println("## Key Commands")
	fmt.Println("- `gt merge queue` - Show pending merges")
	fmt.Println("- `gt merge next` - Process next merge")
	fmt.Println()
	fmt.Printf("Rig: %s\n", style.Dim.Render(ctx.Rig))
}

func outputPolecatContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Polecat Context"))
	fmt.Printf("You are polecat **%s** in rig: %s\n\n",
		style.Bold.Render(ctx.Polecat), style.Bold.Render(ctx.Rig))
	fmt.Println("## Startup Protocol")
	fmt.Println("1. Run `gt prime` - loads context and checks mail automatically")
	fmt.Println("2. Check inbox - if mail shown, read with `gt mail read <id>`")
	fmt.Println("3. Look for '📋 Work Assignment' messages for your task")
	fmt.Println("4. If no mail, check `bd list --status=in_progress` for existing work")
	fmt.Println()
	fmt.Println("## Key Commands")
	fmt.Println("- `gt mail inbox` - Check your inbox for work assignments")
	fmt.Println("- `bd show <issue>` - View your assigned issue")
	fmt.Println("- `bd close <issue>` - Mark issue complete")
	fmt.Println("- `gt done` - Signal work ready for merge")
	fmt.Println()
	fmt.Printf("Polecat: %s | Rig: %s\n",
		style.Dim.Render(ctx.Polecat), style.Dim.Render(ctx.Rig))
}

func outputCrewContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Crew Worker Context"))
	fmt.Printf("You are crew worker **%s** in rig: %s\n\n",
		style.Bold.Render(ctx.Polecat), style.Bold.Render(ctx.Rig))
	fmt.Println("## About Crew Workers")
	fmt.Println("- Persistent workspace (not auto-garbage-collected)")
	fmt.Println("- User-managed (not Witness-monitored)")
	fmt.Println("- Long-lived identity across sessions")
	fmt.Println()
	fmt.Println("## Key Commands")
	fmt.Println("- `gt mail inbox` - Check your inbox")
	fmt.Println("- `bd ready` - Available issues")
	fmt.Println("- `bd show <issue>` - View issue details")
	fmt.Println("- `bd close <issue>` - Mark issue complete")
	fmt.Println()
	fmt.Printf("Crew: %s | Rig: %s\n",
		style.Dim.Render(ctx.Polecat), style.Dim.Render(ctx.Rig))
}

func outputUnknownContext(ctx RoleContext) {
	fmt.Printf("%s\n\n", style.Bold.Render("# Gas Town Context"))
	fmt.Println("Could not determine specific role from current directory.")
	fmt.Println()
	if ctx.Rig != "" {
		fmt.Printf("You appear to be in rig: %s\n\n", style.Bold.Render(ctx.Rig))
	}
	fmt.Println("Navigate to a specific agent directory:")
	fmt.Println("- `<rig>/polecats/<name>/` - Polecat role")
	fmt.Println("- `<rig>/witness/rig/` - Witness role")
	fmt.Println("- `<rig>/refinery/rig/` - Refinery role")
	fmt.Println("- Town root or `mayor/` - Mayor role")
	fmt.Println()
	fmt.Printf("Town root: %s\n", style.Dim.Render(ctx.TownRoot))
}

// outputHandoffContent reads and displays the pinned handoff bead for the role.
func outputHandoffContent(ctx RoleContext) {
	if ctx.Role == RoleUnknown {
		return
	}

	// Get role key for handoff bead lookup
	roleKey := string(ctx.Role)

	bd := beads.New(ctx.TownRoot)
	issue, err := bd.FindHandoffBead(roleKey)
	if err != nil {
		// Silently skip if beads lookup fails (might not be a beads repo)
		return
	}
	if issue == nil || issue.Description == "" {
		// No handoff content
		return
	}

	// Display handoff content
	fmt.Println()
	fmt.Printf("%s\n\n", style.Bold.Render("## 🤝 Handoff from Previous Session"))
	fmt.Println(issue.Description)
	fmt.Println()
	fmt.Println(style.Dim.Render("(Clear with: gt rig reset --handoff)"))
}

// runBdPrime runs `bd prime` and outputs the result.
// This provides beads workflow context to the agent.
func runBdPrime(workDir string) {
	cmd := exec.Command("bd", "prime")
	cmd.Dir = workDir

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil // Ignore stderr

	if err := cmd.Run(); err != nil {
		// Silently skip if bd prime fails (beads might not be available)
		return
	}

	output := strings.TrimSpace(stdout.String())
	if output != "" {
		fmt.Println()
		fmt.Println(output)
	}
}

// outputStartupDirective outputs role-specific instructions for the agent.
// This tells agents like Mayor to announce themselves on startup.
func outputStartupDirective(ctx RoleContext) {
	switch ctx.Role {
	case RoleMayor:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are the Mayor. Please:")
		fmt.Println("1. Announce: \"Mayor, checking in.\"")
		fmt.Println("2. Check mail: `gt mail inbox`")
		fmt.Println("3. If there's a 🤝 HANDOFF message, read it and summarize")
		fmt.Println("4. If no mail, await user instruction")
	case RoleWitness:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are the Witness. Please:")
		fmt.Println("1. Announce: \"Witness, checking in.\"")
		fmt.Println("2. Check for handoff: `gt mail inbox` - look for 🤝 HANDOFF messages")
		fmt.Println("3. Check polecat status: `gt polecat list " + ctx.Rig + " --json`")
		fmt.Println("4. Process any lifecycle requests from inbox")
		fmt.Println("5. If polecats stuck/idle, nudge them")
		fmt.Println("6. If all quiet, wait for activity")
	case RolePolecat:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are a polecat. Please:")
		fmt.Printf("1. Announce: \"%s Polecat %s, checking in.\"\n", ctx.Rig, ctx.Polecat)
		fmt.Println("2. Check mail: `gt mail inbox`")
		fmt.Println("3. If assigned work, begin immediately")
		fmt.Println("4. If no work, announce ready and await assignment")
	case RoleRefinery:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are the Refinery. Please:")
		fmt.Println("1. Announce: \"Refinery, checking in.\"")
		fmt.Println("2. Check mail: `gt mail inbox`")
		fmt.Printf("3. Check merge queue: `gt refinery queue %s`\n", ctx.Rig)
		fmt.Println("4. If MRs pending, process them one at a time")
		fmt.Println("5. If no work, monitor for new MRs periodically")
	case RoleCrew:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are a crew worker. Please:")
		fmt.Printf("1. Announce: \"%s Crew %s, checking in.\"\n", ctx.Rig, ctx.Polecat)
		fmt.Println("2. Check mail: `gt mail inbox`")
		fmt.Println("3. If there's a 🤝 HANDOFF message, read it and continue the work")
		fmt.Println("4. If no mail, await user instruction")
	case RoleDeacon:
		fmt.Println()
		fmt.Println("---")
		fmt.Println()
		fmt.Println("**STARTUP PROTOCOL**: You are the Deacon. Please:")
		fmt.Println("1. Announce: \"Deacon, checking in.\"")
		fmt.Println("2. Signal awake: `gt deacon heartbeat \"starting patrol\"`")
		fmt.Println("3. Check for attached patrol: `bd list --status=in_progress --assignee=deacon`")
		fmt.Println("4. If attached: resume from current step")
		fmt.Println("5. If naked: `gt mol bond mol-deacon-patrol`")
		fmt.Println("6. Execute patrol steps until loop-or-exit")
	}
}

// runMailCheckInject runs `gt mail check --inject` and outputs the result.
// This injects any pending mail into the agent's context.
func runMailCheckInject(workDir string) {
	cmd := exec.Command("gt", "mail", "check", "--inject")
	cmd.Dir = workDir

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil // Ignore stderr

	if err := cmd.Run(); err != nil {
		// Silently skip if mail check fails
		return
	}

	output := strings.TrimSpace(stdout.String())
	if output != "" {
		fmt.Println()
		fmt.Println(output)
	}
}

// outputMoleculeContext checks if the agent is working on a molecule step and shows progress.
func outputMoleculeContext(ctx RoleContext) {
	// Applies to polecats, crew workers, and deacon
	if ctx.Role != RolePolecat && ctx.Role != RoleCrew && ctx.Role != RoleDeacon {
		return
	}

	// For Deacon, use special patrol molecule handling
	if ctx.Role == RoleDeacon {
		outputDeaconPatrolContext(ctx)
		return
	}

	// Check for in-progress issues
	b := beads.New(ctx.WorkDir)
	issues, err := b.List(beads.ListOptions{
		Status:   "in_progress",
		Assignee: ctx.Polecat,
		Priority: -1,
	})
	if err != nil || len(issues) == 0 {
		return
	}

	// Check if any in-progress issue is a molecule step
	for _, issue := range issues {
		moleculeID := parseMoleculeMetadata(issue.Description)
		if moleculeID == "" {
			continue
		}

		// Get the parent (root) issue ID
		rootID := issue.Parent
		if rootID == "" {
			continue
		}

		// This is a molecule step - show context
		fmt.Println()
		fmt.Printf("%s\n\n", style.Bold.Render("## 🧬 Molecule Workflow"))
		fmt.Printf("You are working on a molecule step.\n")
		fmt.Printf("  Current step: %s\n", issue.ID)
		fmt.Printf("  Molecule: %s\n", moleculeID)
		fmt.Printf("  Root issue: %s\n\n", rootID)

		// Show molecule progress by finding sibling steps
		showMoleculeProgress(b, rootID)

		fmt.Println()
		fmt.Println("**Molecule Work Loop:**")
		fmt.Println("1. Complete current step, then `bd close " + issue.ID + "`")
		fmt.Println("2. Check for next steps: `bd ready --parent " + rootID + "`")
		fmt.Println("3. Work on next ready step(s)")
		fmt.Println("4. When all steps done, run `gt done`")
		break // Only show context for first molecule step found
	}
}

// parseMoleculeMetadata extracts molecule info from a step's description.
// Looks for lines like:
//
//	instantiated_from: mol-xyz
func parseMoleculeMetadata(description string) string {
	lines := strings.Split(description, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "instantiated_from:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "instantiated_from:"))
		}
	}
	return ""
}

// showMoleculeProgress displays the progress through a molecule's steps.
func showMoleculeProgress(b *beads.Beads, rootID string) {
	if rootID == "" {
		return
	}

	// Find all children of the root issue
	children, err := b.List(beads.ListOptions{
		Parent:   rootID,
		Status:   "all",
		Priority: -1,
	})
	if err != nil || len(children) == 0 {
		return
	}

	total := len(children)
	done := 0
	inProgress := 0
	var readySteps []string

	for _, child := range children {
		switch child.Status {
		case "closed":
			done++
		case "in_progress":
			inProgress++
		case "open":
			// Check if ready (no open dependencies)
			if len(child.DependsOn) == 0 {
				readySteps = append(readySteps, child.ID)
			}
		}
	}

	fmt.Printf("Progress: %d/%d steps complete", done, total)
	if inProgress > 0 {
		fmt.Printf(" (%d in progress)", inProgress)
	}
	fmt.Println()

	if len(readySteps) > 0 {
		fmt.Printf("Ready steps: %s\n", strings.Join(readySteps, ", "))
	}
}

// outputDeaconPatrolContext shows patrol molecule status for the Deacon.
func outputDeaconPatrolContext(ctx RoleContext) {
	b := beads.New(ctx.TownRoot)

	// Check for in-progress patrol steps assigned to deacon
	issues, err := b.List(beads.ListOptions{
		Status:   "in_progress",
		Assignee: "deacon",
		Priority: -1,
	})
	if err != nil {
		// Silently skip if beads lookup fails
		return
	}

	fmt.Println()
	fmt.Printf("%s\n\n", style.Bold.Render("## 🔄 Patrol Status"))

	if len(issues) == 0 {
		// No attached molecule - show "naked" status
		fmt.Println("Status: **Naked** (no patrol molecule attached)")
		fmt.Println()
		fmt.Println("To start patrol:")
		fmt.Println("  gt mol bond mol-deacon-patrol")
		return
	}

	// Find the patrol molecule step we're working on
	for _, issue := range issues {
		// Check if this is a patrol molecule step
		moleculeID := parseMoleculeMetadata(issue.Description)
		if moleculeID == "" {
			continue
		}

		// Get the parent (root) issue ID
		rootID := issue.Parent
		if rootID == "" {
			continue
		}

		// This is a molecule step - show context
		fmt.Println("Status: **Attached** (patrol molecule in progress)")
		fmt.Printf("  Current step: %s\n", issue.ID)
		fmt.Printf("  Molecule: %s\n", moleculeID)
		fmt.Printf("  Root issue: %s\n\n", rootID)

		// Show patrol progress
		showMoleculeProgress(b, rootID)

		fmt.Println()
		fmt.Println("**Patrol Work Loop:**")
		fmt.Println("1. Execute current step: " + issue.Title)
		fmt.Println("2. Close step: `bd close " + issue.ID + "`")
		fmt.Println("3. Check next: `bd ready --parent " + rootID + "`")
		fmt.Println("4. On final step (loop-or-exit): burn and loop or exit")
		return
	}

	// Has issues but none are molecule steps - might be orphaned work
	fmt.Println("Status: **In-progress work** (not a patrol molecule)")
	fmt.Println()
	fmt.Println("To start fresh patrol:")
	fmt.Println("  bd close <in-progress-issues>")
	fmt.Println("  gt mol bond mol-deacon-patrol")
}

// acquireIdentityLock checks and acquires the identity lock for worker roles.
// This prevents multiple agents from claiming the same worker identity.
// Returns an error if another agent already owns this identity.
func acquireIdentityLock(ctx RoleContext) error {
	// Only lock worker roles (polecat, crew)
	// Infrastructure roles (mayor, witness, refinery, deacon) are singletons
	// managed by tmux session names, so they don't need file-based locks
	if ctx.Role != RolePolecat && ctx.Role != RoleCrew {
		return nil
	}

	// Create lock for this worker directory
	l := lock.New(ctx.WorkDir)

	// Determine session ID from environment or context
	sessionID := os.Getenv("TMUX_PANE")
	if sessionID == "" {
		// Fall back to a descriptive identifier
		sessionID = fmt.Sprintf("%s/%s", ctx.Rig, ctx.Polecat)
	}

	// Try to acquire the lock
	if err := l.Acquire(sessionID); err != nil {
		if errors.Is(err, lock.ErrLocked) {
			// Another agent owns this identity
			fmt.Printf("\n%s\n\n", style.Bold.Render("⚠️  IDENTITY COLLISION DETECTED"))
			fmt.Printf("Another agent already claims this worker identity.\n\n")

			// Show lock details
			if info, readErr := l.Read(); readErr == nil {
				fmt.Printf("Lock holder:\n")
				fmt.Printf("  PID: %d\n", info.PID)
				fmt.Printf("  Session: %s\n", info.SessionID)
				fmt.Printf("  Acquired: %s\n", info.AcquiredAt.Format("2006-01-02 15:04:05"))
				fmt.Println()
			}

			fmt.Printf("To resolve:\n")
			fmt.Printf("  1. Find the other session and close it, OR\n")
			fmt.Printf("  2. Run: gt doctor --fix (cleans stale locks)\n")
			fmt.Printf("  3. If lock is stale: rm %s/.gastown/agent.lock\n", ctx.WorkDir)
			fmt.Println()

			return fmt.Errorf("cannot claim identity %s/%s: %w", ctx.Rig, ctx.Polecat, err)
		}
		return fmt.Errorf("acquiring identity lock: %w", err)
	}

	return nil
}

// ensureBeadsRedirect ensures the .beads/redirect file exists for worktree-based roles.
// This handles cases where git clean or other operations delete the redirect file.
func ensureBeadsRedirect(ctx RoleContext) {
	// Only applies to crew and polecat roles (they use shared beads)
	if ctx.Role != RoleCrew && ctx.Role != RolePolecat {
		return
	}

	// Check if redirect already exists
	beadsDir := filepath.Join(ctx.WorkDir, ".beads")
	redirectPath := filepath.Join(beadsDir, "redirect")

	if _, err := os.Stat(redirectPath); err == nil {
		// Redirect exists, nothing to do
		return
	}

	// Determine the correct redirect path based on role and rig structure
	var redirectContent string

	// Get the rig root (parent of crew/ or polecats/)
	var rigRoot string
	relPath, err := filepath.Rel(ctx.TownRoot, ctx.WorkDir)
	if err != nil {
		return
	}
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) >= 1 {
		rigRoot = filepath.Join(ctx.TownRoot, parts[0])
	} else {
		return
	}

	// Check for shared beads locations in order of preference:
	// 1. rig/mayor/rig/.beads/ (if mayor rig clone exists)
	// 2. rig/.beads/ (rig root beads)
	mayorRigBeads := filepath.Join(rigRoot, "mayor", "rig", ".beads")
	rigRootBeads := filepath.Join(rigRoot, ".beads")

	if _, err := os.Stat(mayorRigBeads); err == nil {
		// Use mayor/rig/.beads
		if ctx.Role == RoleCrew {
			// crew/<name>/.beads -> ../../mayor/rig/.beads
			redirectContent = "../../mayor/rig/.beads"
		} else {
			// polecats/<name>/.beads -> ../../mayor/rig/.beads
			redirectContent = "../../mayor/rig/.beads"
		}
	} else if _, err := os.Stat(rigRootBeads); err == nil {
		// Use rig root .beads
		if ctx.Role == RoleCrew {
			// crew/<name>/.beads -> ../../.beads
			redirectContent = "../../.beads"
		} else {
			// polecats/<name>/.beads -> ../../.beads
			redirectContent = "../../.beads"
		}
	} else {
		// No shared beads found, nothing to redirect to
		return
	}

	// Create .beads directory if needed
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		// Silently fail - not critical
		return
	}

	// Write redirect file
	if err := os.WriteFile(redirectPath, []byte(redirectContent+"\n"), 0644); err != nil {
		// Silently fail - not critical
		return
	}

	// Note: We don't print a message here to avoid cluttering prime output
	// The redirect is silently restored
}
