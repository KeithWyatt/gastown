package cmd

import (
	"github.com/spf13/cobra"
)

// Crew command flags
var (
	crewRig     string
	crewBranch  bool
	crewJSON    bool
	crewForce   bool
	crewNoTmux  bool
	crewMessage string
)

var crewCmd = &cobra.Command{
	Use:   "crew",
	Short: "Manage crew workspaces (user-managed persistent workspaces)",
	Long: `Crew workers are user-managed persistent workspaces within a rig.

Unlike polecats which are witness-managed and transient, crew workers are:
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
  gt crew restart <name>   Kill and restart session fresh (alias: rs)
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

var crewRestartCmd = &cobra.Command{
	Use:     "restart <name>",
	Aliases: []string{"rs"},
	Short:   "Kill and restart crew workspace session",
	Long: `Kill the tmux session and restart fresh with Claude.

Useful when a crew member gets confused or needs a clean slate.
Unlike 'refresh', this does NOT send handoff mail - it's a clean start.

The command will:
1. Kill existing tmux session if running
2. Start fresh session with Claude
3. Run gt prime to reinitialize context

Examples:
  gt crew restart dave            # Restart dave's session
  gt crew rs emma                 # Same, using alias`,
	Args: cobra.ExactArgs(1),
	RunE: runCrewRestart,
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

	crewRestartCmd.Flags().StringVar(&crewRig, "rig", "", "Rig to use")

	// Add subcommands
	crewCmd.AddCommand(crewAddCmd)
	crewCmd.AddCommand(crewListCmd)
	crewCmd.AddCommand(crewAtCmd)
	crewCmd.AddCommand(crewRemoveCmd)
	crewCmd.AddCommand(crewRefreshCmd)
	crewCmd.AddCommand(crewStatusCmd)
	crewCmd.AddCommand(crewRenameCmd)
	crewCmd.AddCommand(crewPristineCmd)
	crewCmd.AddCommand(crewRestartCmd)

	rootCmd.AddCommand(crewCmd)
}
