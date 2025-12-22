package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/doctor"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	doctorFix     bool
	doctorVerbose bool
	doctorRig     string
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run health checks on the workspace",
	Long: `Run diagnostic checks on the Gas Town workspace.

Doctor checks for common configuration issues, missing files,
and other problems that could affect workspace operation.

Use --fix to attempt automatic fixes for issues that support it.
Use --rig to check a specific rig instead of the entire workspace.`,
	RunE: runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorFix, "fix", false, "Attempt to automatically fix issues")
	doctorCmd.Flags().BoolVarP(&doctorVerbose, "verbose", "v", false, "Show detailed output")
	doctorCmd.Flags().StringVar(&doctorRig, "rig", "", "Check specific rig only")
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, args []string) error {
	// Find town root
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Create check context
	ctx := &doctor.CheckContext{
		TownRoot: townRoot,
		RigName:  doctorRig,
		Verbose:  doctorVerbose,
	}

	// Create doctor and register checks
	d := doctor.NewDoctor()

	// Register built-in checks
	d.Register(doctor.NewTownGitCheck())
	d.Register(doctor.NewDaemonCheck())
	d.Register(doctor.NewBeadsDatabaseCheck())
	d.Register(doctor.NewOrphanSessionCheck())
	d.Register(doctor.NewOrphanProcessCheck())
	d.Register(doctor.NewBranchCheck())
	d.Register(doctor.NewBeadsSyncOrphanCheck())
	d.Register(doctor.NewIdentityCollisionCheck())
	d.Register(doctor.NewThemeCheck())

	// Wisp storage checks
	d.Register(doctor.NewWispExistsCheck())
	d.Register(doctor.NewWispGitCheck())
	d.Register(doctor.NewWispOrphansCheck())
	d.Register(doctor.NewWispSizeCheck())
	d.Register(doctor.NewWispStaleCheck())

	// Run checks
	var report *doctor.Report
	if doctorFix {
		report = d.Fix(ctx)
	} else {
		report = d.Run(ctx)
	}

	// Print report
	report.Print(os.Stdout, doctorVerbose)

	// Exit with error code if there are errors
	if report.HasErrors() {
		return fmt.Errorf("doctor found %d error(s)", report.Summary.Errors)
	}

	return nil
}
