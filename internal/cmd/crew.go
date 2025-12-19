package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/crew"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Crew command flags
var (
	crewRig       string
	crewBranch    bool
	crewJSON      bool
	crewForce     bool
	crewNoTmux    bool
	crewMessage   string
)

var crewCmd = &cobra.Command{
	Use:   "crew",
	Short: "Manage crew workspaces (user-managed persistent workspaces)",
	Long: `Crew workers are user-managed persistent workspaces within a rig.

Unlike polecats which are witness-managed and ephemeral, crew workers are:
- Persistent: Not auto-garbage-collected
- User-managed: Overseer controls lifecycle
- Long-lived identities: recognizable names like dave, emma, fred
- Gas Town integrated: Mail, handoff mechanics work
- Tmux optional: Can work in terminal directly

Commands:
  gt crew add <name>       Create a new crew workspace
  gt crew list             List crew workspaces with status
  gt crew at <name>        Attach to crew workspace session
  gt crew remove <name>    Remove a crew workspace
  gt crew refresh <name>   Context cycling with mail-to-self handoff
  gt crew status [<name>]  Show detailed workspace status`,
}

var crewAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Create a new crew workspace",
	Long: `Create a new crew workspace with a clone of the rig repository.

The workspace is created at <rig>/crew/<name>/ with:
- A full git clone of the project repository
- Mail directory for message delivery
- CLAUDE.md with crew worker prompting
- Optional feature branch (crew/<name>)

Examples:
  gt crew add dave                    # Create in current rig
  gt crew add emma --rig gastown      # Create in specific rig
  gt crew add fred --branch           # Create with feature branch`,
	Args: cobra.ExactArgs(1),
	RunE: runCrewAdd,
}

var crewListCmd = &cobra.Command{
	Use:   "list",
	Short: "List crew workspaces with status",
	Long: `List all crew workspaces in a rig with their status.

Shows git branch, session state, and git status for each workspace.

Examples:
  gt crew list                    # List in current rig
  gt crew list --rig gastown      # List in specific rig
  gt crew list --json             # JSON output`,
	RunE: runCrewList,
}

var crewAtCmd = &cobra.Command{
	Use:     "at [name]",
	Aliases: []string{"attach"},
	Short:   "Attach to crew workspace session",
	Long: `Start or attach to a tmux session for a crew workspace.

Creates a new tmux session if none exists, or attaches to existing.
Use --no-tmux to just print the directory path instead.

Role Discovery:
  If no name is provided, attempts to detect the crew workspace from the
  current directory. If you're in <rig>/crew/<name>/, it will attach to
  that workspace automatically.

Examples:
  gt crew at dave                 # Attach to dave's session
  gt crew at                      # Auto-detect from cwd
  gt crew at dave --no-tmux       # Just print path`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCrewAt,
}

var crewRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a crew workspace",
	Long: `Remove a crew workspace from the rig.

Checks for uncommitted changes and running sessions before removing.
Use --force to skip checks and remove anyway.

Examples:
  gt crew remove dave             # Remove with safety checks
  gt crew remove dave --force     # Force remove`,
	Args: cobra.ExactArgs(1),
	RunE: runCrewRemove,
}

var crewRefreshCmd = &cobra.Command{
	Use:   "refresh <name>",
	Short: "Context cycling with mail-to-self handoff",
	Long: `Cycle a crew workspace session with handoff.

Sends a handoff mail to the workspace's own inbox, then restarts the session.
The new session reads the handoff mail and resumes work.

Examples:
  gt crew refresh dave                           # Refresh with auto-generated handoff
  gt crew refresh dave -m "Working on gt-123"    # Add custom message`,
	Args: cobra.ExactArgs(1),
	RunE: runCrewRefresh,
}

var crewStatusCmd = &cobra.Command{
	Use:   "status [<name>]",
	Short: "Show detailed workspace status",
	Long: `Show detailed status for crew workspace(s).

Displays session state, git status, branch info, and mail inbox status.
If no name given, shows status for all crew workers.

Examples:
  gt crew status                  # Status of all crew workers
  gt crew status dave             # Status of specific worker
  gt crew status --json           # JSON output`,
	RunE: runCrewStatus,
}

var crewRenameCmd = &cobra.Command{
	Use:   "rename <old-name> <new-name>",
	Short: "Rename a crew workspace",
	Long: `Rename a crew workspace.

Kills any running session, renames the directory, and updates state.
The new session will use the new name (gt-<rig>-crew-<new-name>).

Examples:
  gt crew rename dave david       # Rename dave to david
  gt crew rename madmax max       # Rename madmax to max`,
	Args: cobra.ExactArgs(2),
	RunE: runCrewRename,
}

var crewPristineCmd = &cobra.Command{
	Use:   "pristine [<name>]",
	Short: "Sync crew workspaces with remote",
	Long: `Ensure crew workspace(s) are up-to-date.

Runs git pull and bd sync for the specified crew, or all crew workers.
Reports any uncommitted changes that may need attention.

Examples:
  gt crew pristine                # Pristine all crew workers
  gt crew pristine dave           # Pristine specific worker
  gt crew pristine --json         # JSON output`,
	RunE: runCrewPristine,
}

func init() {
	// Add flags
	crewAddCmd.Flags().StringVar(&crewRig, "rig", "", "Rig to create crew workspace in")
	crewAddCmd.Flags().BoolVar(&crewBranch, "branch", false, "Create a feature branch (crew/<name>)")

	crewListCmd.Flags().StringVar(&crewRig, "rig", "", "Filter by rig name")
	crewListCmd.Flags().BoolVar(&crewJSON, "json", false, "Output as JSON")

	crewAtCmd.Flags().StringVar(&crewRig, "rig", "", "Rig to use")
	crewAtCmd.Flags().BoolVar(&crewNoTmux, "no-tmux", false, "Just print directory path")

	crewRemoveCmd.Flags().StringVar(&crewRig, "rig", "", "Rig to use")
	crewRemoveCmd.Flags().BoolVar(&crewForce, "force", false, "Force remove (skip safety checks)")

	crewRefreshCmd.Flags().StringVar(&crewRig, "rig", "", "Rig to use")
	crewRefreshCmd.Flags().StringVarP(&crewMessage, "message", "m", "", "Custom handoff message")

	crewStatusCmd.Flags().StringVar(&crewRig, "rig", "", "Filter by rig name")
	crewStatusCmd.Flags().BoolVar(&crewJSON, "json", false, "Output as JSON")

	crewRenameCmd.Flags().StringVar(&crewRig, "rig", "", "Rig to use")

	crewPristineCmd.Flags().StringVar(&crewRig, "rig", "", "Filter by rig name")
	crewPristineCmd.Flags().BoolVar(&crewJSON, "json", false, "Output as JSON")

	// Add subcommands
	crewCmd.AddCommand(crewAddCmd)
	crewCmd.AddCommand(crewListCmd)
	crewCmd.AddCommand(crewAtCmd)
	crewCmd.AddCommand(crewRemoveCmd)
	crewCmd.AddCommand(crewRefreshCmd)
	crewCmd.AddCommand(crewStatusCmd)
	crewCmd.AddCommand(crewRenameCmd)
	crewCmd.AddCommand(crewPristineCmd)

	rootCmd.AddCommand(crewCmd)
}

func runCrewAdd(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Find workspace
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Load rigs config
	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		rigsConfig = &config.RigsConfig{Rigs: make(map[string]config.RigEntry)}
	}

	// Determine rig
	rigName := crewRig
	if rigName == "" {
		// Try to infer from cwd
		rigName, err = inferRigFromCwd(townRoot)
		if err != nil {
			return fmt.Errorf("could not determine rig (use --rig flag): %w", err)
		}
	}

	// Get rig
	g := git.NewGit(townRoot)
	rigMgr := rig.NewManager(townRoot, rigsConfig, g)
	r, err := rigMgr.GetRig(rigName)
	if err != nil {
		return fmt.Errorf("rig '%s' not found", rigName)
	}

	// Create crew manager
	crewGit := git.NewGit(r.Path)
	crewMgr := crew.NewManager(r, crewGit)

	// Create crew workspace
	fmt.Printf("Creating crew workspace %s in %s...\n", name, rigName)

	worker, err := crewMgr.Add(name, crewBranch)
	if err != nil {
		if err == crew.ErrCrewExists {
			return fmt.Errorf("crew workspace '%s' already exists", name)
		}
		return fmt.Errorf("creating crew workspace: %w", err)
	}

	fmt.Printf("%s Created crew workspace: %s/%s\n",
		style.Bold.Render("✓"), rigName, name)
	fmt.Printf("  Path: %s\n", worker.ClonePath)
	fmt.Printf("  Branch: %s\n", worker.Branch)
	fmt.Printf("  Mail: %s/mail/\n", worker.ClonePath)

	fmt.Printf("\n%s\n", style.Dim.Render("Start working with: cd "+worker.ClonePath))

	return nil
}

// inferRigFromCwd tries to determine the rig from the current directory.
func inferRigFromCwd(townRoot string) (string, error) {
	cwd, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}

	// Check if cwd is within a rig
	rel, err := filepath.Rel(townRoot, cwd)
	if err != nil {
		return "", fmt.Errorf("not in workspace")
	}

	// Normalize and split path - first component is the rig name
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")

	if len(parts) > 0 && parts[0] != "" && parts[0] != "." {
		return parts[0], nil
	}

	return "", fmt.Errorf("could not infer rig from current directory")
}

// getCrewManager returns a crew manager for the specified or inferred rig.
func getCrewManager(rigName string) (*crew.Manager, *rig.Rig, error) {
	// Find town root
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return nil, nil, fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Load rigs config
	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		rigsConfig = &config.RigsConfig{Rigs: make(map[string]config.RigEntry)}
	}

	// Determine rig
	if rigName == "" {
		rigName, err = inferRigFromCwd(townRoot)
		if err != nil {
			return nil, nil, fmt.Errorf("could not determine rig (use --rig flag): %w", err)
		}
	}

	// Get rig
	g := git.NewGit(townRoot)
	rigMgr := rig.NewManager(townRoot, rigsConfig, g)
	r, err := rigMgr.GetRig(rigName)
	if err != nil {
		return nil, nil, fmt.Errorf("rig '%s' not found", rigName)
	}

	// Create crew manager
	crewGit := git.NewGit(r.Path)
	crewMgr := crew.NewManager(r, crewGit)

	return crewMgr, r, nil
}

// crewSessionName generates the tmux session name for a crew worker.
func crewSessionName(rigName, crewName string) string {
	return fmt.Sprintf("gt-%s-crew-%s", rigName, crewName)
}

// CrewListItem represents a crew worker in list output.
type CrewListItem struct {
	Name       string `json:"name"`
	Rig        string `json:"rig"`
	Branch     string `json:"branch"`
	Path       string `json:"path"`
	HasSession bool   `json:"has_session"`
	GitClean   bool   `json:"git_clean"`
}

func runCrewList(cmd *cobra.Command, args []string) error {
	crewMgr, r, err := getCrewManager(crewRig)
	if err != nil {
		return err
	}

	workers, err := crewMgr.List()
	if err != nil {
		return fmt.Errorf("listing crew workers: %w", err)
	}

	if len(workers) == 0 {
		fmt.Println("No crew workspaces found.")
		return nil
	}

	// Check session and git status for each worker
	t := tmux.NewTmux()
	var items []CrewListItem

	for _, w := range workers {
		sessionID := crewSessionName(r.Name, w.Name)
		hasSession, _ := t.HasSession(sessionID)

		crewGit := git.NewGit(w.ClonePath)
		gitClean := true
		if status, err := crewGit.Status(); err == nil {
			gitClean = status.Clean
		}

		items = append(items, CrewListItem{
			Name:       w.Name,
			Rig:        r.Name,
			Branch:     w.Branch,
			Path:       w.ClonePath,
			HasSession: hasSession,
			GitClean:   gitClean,
		})
	}

	if crewJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	}

	// Text output
	fmt.Printf("%s\n\n", style.Bold.Render("Crew Workspaces"))
	for _, item := range items {
		status := style.Dim.Render("○")
		if item.HasSession {
			status = style.Bold.Render("●")
		}

		gitStatus := style.Dim.Render("clean")
		if !item.GitClean {
			gitStatus = style.Bold.Render("dirty")
		}

		fmt.Printf("  %s %s/%s\n", status, item.Rig, item.Name)
		fmt.Printf("    Branch: %s  Git: %s\n", item.Branch, gitStatus)
		fmt.Printf("    %s\n", style.Dim.Render(item.Path))
	}

	return nil
}

func runCrewAt(cmd *cobra.Command, args []string) error {
	var name string

	// Determine crew name: from arg, or auto-detect from cwd
	if len(args) > 0 {
		name = args[0]
	} else {
		// Try to detect from current directory
		detected, err := detectCrewFromCwd()
		if err != nil {
			return fmt.Errorf("could not detect crew workspace from current directory: %w\n\nUsage: gt crew at <name>", err)
		}
		name = detected.crewName
		if crewRig == "" {
			crewRig = detected.rigName
		}
		fmt.Printf("Detected crew workspace: %s/%s\n", detected.rigName, name)
	}

	crewMgr, r, err := getCrewManager(crewRig)
	if err != nil {
		return err
	}

	// Get the crew worker
	worker, err := crewMgr.Get(name)
	if err != nil {
		if err == crew.ErrCrewNotFound {
			return fmt.Errorf("crew workspace '%s' not found", name)
		}
		return fmt.Errorf("getting crew worker: %w", err)
	}

	// If --no-tmux, just print the path
	if crewNoTmux {
		fmt.Println(worker.ClonePath)
		return nil
	}

	// Check if session exists
	t := tmux.NewTmux()
	sessionID := crewSessionName(r.Name, name)
	hasSession, err := t.HasSession(sessionID)
	if err != nil {
		return fmt.Errorf("checking session: %w", err)
	}

	if !hasSession {
		// Create new session
		if err := t.NewSession(sessionID, worker.ClonePath); err != nil {
			return fmt.Errorf("creating session: %w", err)
		}

		// Set environment
		t.SetEnvironment(sessionID, "GT_RIG", r.Name)
		t.SetEnvironment(sessionID, "GT_CREW", name)

		// Start claude with skip permissions (crew workers are trusted like Mayor)
		if err := t.SendKeys(sessionID, "claude --dangerously-skip-permissions"); err != nil {
			return fmt.Errorf("starting claude: %w", err)
		}

		// Wait a moment for Claude to initialize, then prime it
		// We send gt prime after a short delay to ensure Claude is ready
		if err := t.SendKeysDelayed(sessionID, "gt prime", 2000); err != nil {
			// Non-fatal: Claude started but priming failed
			fmt.Printf("Warning: Could not send prime command: %v\n", err)
		}

		fmt.Printf("%s Created session for %s/%s\n",
			style.Bold.Render("✓"), r.Name, name)
	} else {
		// Session exists - check if Claude is still running
		paneCmd, err := t.GetPaneCommand(sessionID)
		if err == nil && isShellCommand(paneCmd) {
			// Claude has exited, restart it
			fmt.Printf("Claude exited, restarting...\n")
			if err := t.SendKeys(sessionID, "claude --dangerously-skip-permissions"); err != nil {
				return fmt.Errorf("restarting claude: %w", err)
			}
			// Prime after restart
			if err := t.SendKeysDelayed(sessionID, "gt prime", 2000); err != nil {
				fmt.Printf("Warning: Could not send prime command: %v\n", err)
			}
		}
	}

	// Attach to session using exec to properly forward TTY
	return attachToTmuxSession(sessionID)
}

// isShellCommand checks if the command is a shell (meaning Claude has exited).
func isShellCommand(cmd string) bool {
	shells := []string{"bash", "zsh", "sh", "fish", "tcsh", "ksh"}
	for _, shell := range shells {
		if cmd == shell {
			return true
		}
	}
	return false
}

// attachToTmuxSession attaches to a tmux session with proper TTY forwarding.
func attachToTmuxSession(sessionID string) error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}

	cmd := exec.Command(tmuxPath, "attach-session", "-t", sessionID)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// crewDetection holds the result of detecting crew workspace from cwd.
type crewDetection struct {
	rigName  string
	crewName string
}

// detectCrewFromCwd attempts to detect the crew workspace from the current directory.
// It looks for the pattern <town>/<rig>/crew/<name>/ in the current path.
func detectCrewFromCwd() (*crewDetection, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting cwd: %w", err)
	}

	// Find town root
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return nil, fmt.Errorf("not in Gas Town workspace: %w", err)
	}
	if townRoot == "" {
		return nil, fmt.Errorf("not in Gas Town workspace")
	}

	// Get relative path from town root
	relPath, err := filepath.Rel(townRoot, cwd)
	if err != nil {
		return nil, fmt.Errorf("getting relative path: %w", err)
	}

	// Normalize and split path
	relPath = filepath.ToSlash(relPath)
	parts := strings.Split(relPath, "/")

	// Look for pattern: <rig>/crew/<name>/...
	// Minimum: rig, crew, name = 3 parts
	if len(parts) < 3 {
		return nil, fmt.Errorf("not in a crew workspace (path too short)")
	}

	rigName := parts[0]
	if parts[1] != "crew" {
		return nil, fmt.Errorf("not in a crew workspace (not in crew/ directory)")
	}
	crewName := parts[2]

	return &crewDetection{
		rigName:  rigName,
		crewName: crewName,
	}, nil
}

func runCrewRemove(cmd *cobra.Command, args []string) error {
	name := args[0]

	crewMgr, r, err := getCrewManager(crewRig)
	if err != nil {
		return err
	}

	// Check for running session (unless forced)
	if !crewForce {
		t := tmux.NewTmux()
		sessionID := crewSessionName(r.Name, name)
		hasSession, _ := t.HasSession(sessionID)
		if hasSession {
			return fmt.Errorf("session '%s' is running (use --force to kill and remove)", sessionID)
		}
	}

	// Kill session if it exists
	t := tmux.NewTmux()
	sessionID := crewSessionName(r.Name, name)
	if hasSession, _ := t.HasSession(sessionID); hasSession {
		if err := t.KillSession(sessionID); err != nil {
			return fmt.Errorf("killing session: %w", err)
		}
		fmt.Printf("Killed session %s\n", sessionID)
	}

	// Remove the crew workspace
	if err := crewMgr.Remove(name, crewForce); err != nil {
		if err == crew.ErrCrewNotFound {
			return fmt.Errorf("crew workspace '%s' not found", name)
		}
		if err == crew.ErrHasChanges {
			return fmt.Errorf("crew workspace has uncommitted changes (use --force to remove anyway)")
		}
		return fmt.Errorf("removing crew workspace: %w", err)
	}

	fmt.Printf("%s Removed crew workspace: %s/%s\n",
		style.Bold.Render("✓"), r.Name, name)
	return nil
}

func runCrewRefresh(cmd *cobra.Command, args []string) error {
	name := args[0]

	crewMgr, r, err := getCrewManager(crewRig)
	if err != nil {
		return err
	}

	// Get the crew worker
	worker, err := crewMgr.Get(name)
	if err != nil {
		if err == crew.ErrCrewNotFound {
			return fmt.Errorf("crew workspace '%s' not found", name)
		}
		return fmt.Errorf("getting crew worker: %w", err)
	}

	t := tmux.NewTmux()
	sessionID := crewSessionName(r.Name, name)

	// Check if session exists
	hasSession, _ := t.HasSession(sessionID)

	// Create handoff message
	handoffMsg := crewMessage
	if handoffMsg == "" {
		handoffMsg = fmt.Sprintf("Context refresh for %s. Check mail and beads for current work state.", name)
	}

	// Send handoff mail to self
	mailDir := filepath.Join(worker.ClonePath, "mail")
	if _, err := os.Stat(mailDir); os.IsNotExist(err) {
		if err := os.MkdirAll(mailDir, 0755); err != nil {
			return fmt.Errorf("creating mail dir: %w", err)
		}
	}

	// Create and send mail
	mailbox := mail.NewMailbox(mailDir)
	msg := &mail.Message{
		From:    fmt.Sprintf("%s/%s", r.Name, name),
		To:      fmt.Sprintf("%s/%s", r.Name, name),
		Subject: "🤝 HANDOFF: Context Refresh",
		Body:    handoffMsg,
	}
	if err := mailbox.Append(msg); err != nil {
		return fmt.Errorf("sending handoff mail: %w", err)
	}
	fmt.Printf("Sent handoff mail to %s/%s\n", r.Name, name)

	// Kill existing session if running
	if hasSession {
		if err := t.KillSession(sessionID); err != nil {
			return fmt.Errorf("killing old session: %w", err)
		}
		fmt.Printf("Killed old session %s\n", sessionID)
	}

	// Start new session
	if err := t.NewSession(sessionID, worker.ClonePath); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	// Set environment
	t.SetEnvironment(sessionID, "GT_RIG", r.Name)
	t.SetEnvironment(sessionID, "GT_CREW", name)

	// Start claude
	if err := t.SendKeys(sessionID, "claude"); err != nil {
		return fmt.Errorf("starting claude: %w", err)
	}

	fmt.Printf("%s Refreshed crew workspace: %s/%s\n",
		style.Bold.Render("✓"), r.Name, name)
	fmt.Printf("Attach with: %s\n", style.Dim.Render(fmt.Sprintf("gt crew at %s", name)))

	return nil
}

// CrewStatusItem represents detailed status for a crew worker.
type CrewStatusItem struct {
	Name        string   `json:"name"`
	Rig         string   `json:"rig"`
	Path        string   `json:"path"`
	Branch      string   `json:"branch"`
	HasSession  bool     `json:"has_session"`
	SessionID   string   `json:"session_id,omitempty"`
	GitClean    bool     `json:"git_clean"`
	GitModified []string `json:"git_modified,omitempty"`
	GitUntracked []string `json:"git_untracked,omitempty"`
	MailTotal   int      `json:"mail_total"`
	MailUnread  int      `json:"mail_unread"`
}

func runCrewStatus(cmd *cobra.Command, args []string) error {
	crewMgr, r, err := getCrewManager(crewRig)
	if err != nil {
		return err
	}

	var workers []*crew.CrewWorker

	if len(args) > 0 {
		// Specific worker
		name := args[0]
		worker, err := crewMgr.Get(name)
		if err != nil {
			if err == crew.ErrCrewNotFound {
				return fmt.Errorf("crew workspace '%s' not found", name)
			}
			return fmt.Errorf("getting crew worker: %w", err)
		}
		workers = []*crew.CrewWorker{worker}
	} else {
		// All workers
		workers, err = crewMgr.List()
		if err != nil {
			return fmt.Errorf("listing crew workers: %w", err)
		}
	}

	if len(workers) == 0 {
		fmt.Println("No crew workspaces found.")
		return nil
	}

	t := tmux.NewTmux()
	var items []CrewStatusItem

	for _, w := range workers {
		sessionID := crewSessionName(r.Name, w.Name)
		hasSession, _ := t.HasSession(sessionID)

		// Git status
		crewGit := git.NewGit(w.ClonePath)
		gitStatus, _ := crewGit.Status()
		branch, _ := crewGit.CurrentBranch()

		gitClean := true
		var modified, untracked []string
		if gitStatus != nil {
			gitClean = gitStatus.Clean
			modified = append(gitStatus.Modified, gitStatus.Added...)
			modified = append(modified, gitStatus.Deleted...)
			untracked = gitStatus.Untracked
		}

		// Mail status
		mailDir := filepath.Join(w.ClonePath, "mail")
		mailTotal, mailUnread := 0, 0
		if _, err := os.Stat(mailDir); err == nil {
			mailbox := mail.NewMailbox(mailDir)
			mailTotal, mailUnread, _ = mailbox.Count()
		}

		item := CrewStatusItem{
			Name:         w.Name,
			Rig:          r.Name,
			Path:         w.ClonePath,
			Branch:       branch,
			HasSession:   hasSession,
			GitClean:     gitClean,
			GitModified:  modified,
			GitUntracked: untracked,
			MailTotal:    mailTotal,
			MailUnread:   mailUnread,
		}
		if hasSession {
			item.SessionID = sessionID
		}

		items = append(items, item)
	}

	if crewJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	}

	// Text output
	for i, item := range items {
		if i > 0 {
			fmt.Println()
		}

		sessionStatus := style.Dim.Render("○ stopped")
		if item.HasSession {
			sessionStatus = style.Bold.Render("● running")
		}

		fmt.Printf("%s %s/%s\n", sessionStatus, item.Rig, item.Name)
		fmt.Printf("  Path:   %s\n", item.Path)
		fmt.Printf("  Branch: %s\n", item.Branch)

		if item.GitClean {
			fmt.Printf("  Git:    %s\n", style.Dim.Render("clean"))
		} else {
			fmt.Printf("  Git:    %s\n", style.Bold.Render("dirty"))
			if len(item.GitModified) > 0 {
				fmt.Printf("          Modified: %s\n", strings.Join(item.GitModified, ", "))
			}
			if len(item.GitUntracked) > 0 {
				fmt.Printf("          Untracked: %s\n", strings.Join(item.GitUntracked, ", "))
			}
		}

		if item.MailUnread > 0 {
			fmt.Printf("  Mail:   %d unread / %d total\n", item.MailUnread, item.MailTotal)
		} else {
			fmt.Printf("  Mail:   %s\n", style.Dim.Render(fmt.Sprintf("%d messages", item.MailTotal)))
		}
	}

	return nil
}

func runCrewRename(cmd *cobra.Command, args []string) error {
	oldName := args[0]
	newName := args[1]

	crewMgr, r, err := getCrewManager(crewRig)
	if err != nil {
		return err
	}

	// Kill any running session for the old name
	t := tmux.NewTmux()
	oldSessionID := crewSessionName(r.Name, oldName)
	if hasSession, _ := t.HasSession(oldSessionID); hasSession {
		if err := t.KillSession(oldSessionID); err != nil {
			return fmt.Errorf("killing old session: %w", err)
		}
		fmt.Printf("Killed session %s\n", oldSessionID)
	}

	// Perform the rename
	if err := crewMgr.Rename(oldName, newName); err != nil {
		if err == crew.ErrCrewNotFound {
			return fmt.Errorf("crew workspace '%s' not found", oldName)
		}
		if err == crew.ErrCrewExists {
			return fmt.Errorf("crew workspace '%s' already exists", newName)
		}
		return fmt.Errorf("renaming crew workspace: %w", err)
	}

	fmt.Printf("%s Renamed crew workspace: %s/%s → %s/%s\n",
		style.Bold.Render("✓"), r.Name, oldName, r.Name, newName)
	fmt.Printf("New session will be: %s\n", style.Dim.Render(crewSessionName(r.Name, newName)))

	return nil
}

func runCrewPristine(cmd *cobra.Command, args []string) error {
	crewMgr, r, err := getCrewManager(crewRig)
	if err != nil {
		return err
	}

	var workers []*crew.CrewWorker

	if len(args) > 0 {
		// Specific worker
		name := args[0]
		worker, err := crewMgr.Get(name)
		if err != nil {
			if err == crew.ErrCrewNotFound {
				return fmt.Errorf("crew workspace '%s' not found", name)
			}
			return fmt.Errorf("getting crew worker: %w", err)
		}
		workers = []*crew.CrewWorker{worker}
	} else {
		// All workers
		workers, err = crewMgr.List()
		if err != nil {
			return fmt.Errorf("listing crew workers: %w", err)
		}
	}

	if len(workers) == 0 {
		fmt.Println("No crew workspaces found.")
		return nil
	}

	var results []*crew.PristineResult

	for _, w := range workers {
		result, err := crewMgr.Pristine(w.Name)
		if err != nil {
			return fmt.Errorf("pristine %s: %w", w.Name, err)
		}
		results = append(results, result)
	}

	if crewJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}

	// Text output
	for _, result := range results {
		fmt.Printf("%s %s/%s\n", style.Bold.Render("→"), r.Name, result.Name)

		if result.HadChanges {
			fmt.Printf("  %s\n", style.Bold.Render("⚠ Has uncommitted changes"))
		}

		if result.Pulled {
			fmt.Printf("  %s git pull\n", style.Dim.Render("✓"))
		} else if result.PullError != "" {
			fmt.Printf("  %s git pull: %s\n", style.Bold.Render("✗"), result.PullError)
		}

		if result.Synced {
			fmt.Printf("  %s bd sync\n", style.Dim.Render("✓"))
		} else if result.SyncError != "" {
			fmt.Printf("  %s bd sync: %s\n", style.Bold.Render("✗"), result.SyncError)
		}
	}

	return nil
}
