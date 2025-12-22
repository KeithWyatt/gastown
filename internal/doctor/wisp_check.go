package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/steveyegge/gastown/internal/config"
)

// WispExistsCheck verifies that .beads-wisp/ exists for each rig.
type WispExistsCheck struct {
	FixableCheck
	missingRigs []string // Cached for fix
}

// NewWispExistsCheck creates a new wisp exists check.
func NewWispExistsCheck() *WispExistsCheck {
	return &WispExistsCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "wisp-exists",
				CheckDescription: "Check if wisp directory exists for each rig",
			},
		},
	}
}

// Run checks if .beads-wisp/ exists for each rig.
func (c *WispExistsCheck) Run(ctx *CheckContext) *CheckResult {
	c.missingRigs = nil // Reset cache

	// Find all rigs
	rigs, err := c.discoverRigs(ctx.TownRoot)
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusError,
			Message: "Failed to discover rigs",
			Details: []string{err.Error()},
		}
	}

	if len(rigs) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No rigs configured",
		}
	}

	// Check each rig
	var missing []string
	for _, rigName := range rigs {
		wispPath := filepath.Join(ctx.TownRoot, rigName, ".beads-wisp")
		if _, err := os.Stat(wispPath); os.IsNotExist(err) {
			missing = append(missing, rigName)
		}
	}

	if len(missing) > 0 {
		c.missingRigs = missing
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("%d rig(s) missing wisp directory", len(missing)),
			Details: missing,
			FixHint: "Run 'gt doctor --fix' to create missing directories",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusOK,
		Message: fmt.Sprintf("All %d rig(s) have wisp directory", len(rigs)),
	}
}

// Fix creates missing .beads-wisp/ directories.
func (c *WispExistsCheck) Fix(ctx *CheckContext) error {
	for _, rigName := range c.missingRigs {
		wispPath := filepath.Join(ctx.TownRoot, rigName, ".beads-wisp")
		if err := os.MkdirAll(wispPath, 0755); err != nil {
			return fmt.Errorf("creating %s: %w", wispPath, err)
		}
	}
	return nil
}

// discoverRigs finds all registered rigs.
func (c *WispExistsCheck) discoverRigs(townRoot string) ([]string, error) {
	rigsPath := filepath.Join(townRoot, "mayor", "rigs.json")
	data, err := os.ReadFile(rigsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No rigs configured
		}
		return nil, err
	}

	var rigsConfig config.RigsConfig
	if err := json.Unmarshal(data, &rigsConfig); err != nil {
		return nil, err
	}

	var rigs []string
	for name := range rigsConfig.Rigs {
		rigs = append(rigs, name)
	}
	return rigs, nil
}

// WispGitCheck verifies that .beads-wisp/ is a valid git repo.
type WispGitCheck struct {
	FixableCheck
	invalidRigs []string // Cached for fix
}

// NewWispGitCheck creates a new wisp git check.
func NewWispGitCheck() *WispGitCheck {
	return &WispGitCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "wisp-git",
				CheckDescription: "Check if wisp directories are valid git repos",
			},
		},
	}
}

// Run checks if .beads-wisp/ directories are valid git repos.
func (c *WispGitCheck) Run(ctx *CheckContext) *CheckResult {
	c.invalidRigs = nil // Reset cache

	// Find all rigs
	rigs, err := discoverRigs(ctx.TownRoot)
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusError,
			Message: "Failed to discover rigs",
			Details: []string{err.Error()},
		}
	}

	if len(rigs) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No rigs configured",
		}
	}

	// Check each rig that has a wisp dir
	var invalid []string
	var checked int
	for _, rigName := range rigs {
		wispPath := filepath.Join(ctx.TownRoot, rigName, ".beads-wisp")
		if _, err := os.Stat(wispPath); os.IsNotExist(err) {
			continue // Skip if directory doesn't exist (handled by wisp-exists)
		}
		checked++

		// Check if it's a valid git repo
		gitDir := filepath.Join(wispPath, ".git")
		if _, err := os.Stat(gitDir); os.IsNotExist(err) {
			invalid = append(invalid, rigName)
		}
	}

	if checked == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No wisp directories to check",
		}
	}

	if len(invalid) > 0 {
		c.invalidRigs = invalid
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("%d wisp directory(ies) not initialized as git", len(invalid)),
			Details: invalid,
			FixHint: "Run 'gt doctor --fix' to initialize git repos",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusOK,
		Message: fmt.Sprintf("All %d wisp directories are valid git repos", checked),
	}
}

// Fix initializes git repos in wisp directories.
func (c *WispGitCheck) Fix(ctx *CheckContext) error {
	for _, rigName := range c.invalidRigs {
		wispPath := filepath.Join(ctx.TownRoot, rigName, ".beads-wisp")
		cmd := exec.Command("git", "init")
		cmd.Dir = wispPath
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("initializing git in %s: %w", wispPath, err)
		}

		// Create config.yaml for wisp storage
		configPath := filepath.Join(wispPath, "config.yaml")
		configContent := "wisp: true\n# No sync-branch - wisps are local only\n"
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			return fmt.Errorf("creating config.yaml in %s: %w", wispPath, err)
		}
	}
	return nil
}

// WispOrphansCheck detects molecules started but never squashed (>24h old).
type WispOrphansCheck struct {
	BaseCheck
}

// NewWispOrphansCheck creates a new wisp orphans check.
func NewWispOrphansCheck() *WispOrphansCheck {
	return &WispOrphansCheck{
		BaseCheck: BaseCheck{
			CheckName:        "wisp-orphans",
			CheckDescription: "Check for orphaned wisps (>24h old, never squashed)",
		},
	}
}

// Run checks for orphaned wisps.
func (c *WispOrphansCheck) Run(ctx *CheckContext) *CheckResult {
	rigs, err := discoverRigs(ctx.TownRoot)
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusError,
			Message: "Failed to discover rigs",
			Details: []string{err.Error()},
		}
	}

	if len(rigs) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No rigs configured",
		}
	}

	var orphans []string
	cutoff := time.Now().Add(-24 * time.Hour)

	for _, rigName := range rigs {
		wispPath := filepath.Join(ctx.TownRoot, rigName, ".beads-wisp")
		if _, err := os.Stat(wispPath); os.IsNotExist(err) {
			continue
		}

		// Look for molecule directories or issue files older than 24h
		issuesPath := filepath.Join(wispPath, "issues.jsonl")
		info, err := os.Stat(issuesPath)
		if err != nil {
			continue // No issues file
		}

		// Check if the issues file is old and non-empty
		if info.ModTime().Before(cutoff) && info.Size() > 0 {
			orphans = append(orphans, fmt.Sprintf("%s: issues.jsonl last modified %s",
				rigName, info.ModTime().Format("2006-01-02 15:04")))
		}
	}

	if len(orphans) > 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("%d rig(s) have stale wisp data (>24h old)", len(orphans)),
			Details: orphans,
			FixHint: "Manual review required - these may contain unsquashed work",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusOK,
		Message: "No orphaned wisps found",
	}
}

// WispSizeCheck warns if wisp repo is too large (>100MB).
type WispSizeCheck struct {
	BaseCheck
}

// NewWispSizeCheck creates a new wisp size check.
func NewWispSizeCheck() *WispSizeCheck {
	return &WispSizeCheck{
		BaseCheck: BaseCheck{
			CheckName:        "wisp-size",
			CheckDescription: "Check if wisp directories are too large (>100MB)",
		},
	}
}

// Run checks the size of wisp directories.
func (c *WispSizeCheck) Run(ctx *CheckContext) *CheckResult {
	rigs, err := discoverRigs(ctx.TownRoot)
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusError,
			Message: "Failed to discover rigs",
			Details: []string{err.Error()},
		}
	}

	if len(rigs) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No rigs configured",
		}
	}

	const maxSize = 100 * 1024 * 1024 // 100MB
	var oversized []string

	for _, rigName := range rigs {
		wispPath := filepath.Join(ctx.TownRoot, rigName, ".beads-wisp")
		if _, err := os.Stat(wispPath); os.IsNotExist(err) {
			continue
		}

		size, err := dirSize(wispPath)
		if err != nil {
			continue
		}

		if size > maxSize {
			oversized = append(oversized, fmt.Sprintf("%s: %s",
				rigName, formatSize(size)))
		}
	}

	if len(oversized) > 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("%d rig(s) have oversized wisp directories", len(oversized)),
			Details: oversized,
			FixHint: "Consider cleaning up old completed molecules",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusOK,
		Message: "All wisp directories within size limits",
	}
}

// WispStaleCheck detects molecules with no activity in the last hour.
type WispStaleCheck struct {
	BaseCheck
}

// NewWispStaleCheck creates a new wisp stale check.
func NewWispStaleCheck() *WispStaleCheck {
	return &WispStaleCheck{
		BaseCheck: BaseCheck{
			CheckName:        "wisp-stale",
			CheckDescription: "Check for stale wisps (no activity in last hour)",
		},
	}
}

// Run checks for stale wisps.
func (c *WispStaleCheck) Run(ctx *CheckContext) *CheckResult {
	rigs, err := discoverRigs(ctx.TownRoot)
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusError,
			Message: "Failed to discover rigs",
			Details: []string{err.Error()},
		}
	}

	if len(rigs) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No rigs configured",
		}
	}

	var stale []string
	cutoff := time.Now().Add(-1 * time.Hour)

	for _, rigName := range rigs {
		wispPath := filepath.Join(ctx.TownRoot, rigName, ".beads-wisp")
		if _, err := os.Stat(wispPath); os.IsNotExist(err) {
			continue
		}

		// Check for any recent activity in the wisp directory
		// We look at the most recent modification time of any file
		var mostRecent time.Time
		_ = filepath.Walk(wispPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && info.ModTime().After(mostRecent) {
				mostRecent = info.ModTime()
			}
			return nil
		})

		// If there are files and the most recent is older than 1 hour
		if !mostRecent.IsZero() && mostRecent.Before(cutoff) {
			stale = append(stale, fmt.Sprintf("%s: last activity %s ago",
				rigName, formatDuration(time.Since(mostRecent))))
		}
	}

	if len(stale) > 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("%d rig(s) have stale wisp activity", len(stale)),
			Details: stale,
			FixHint: "Check if polecats are stuck or crashed",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusOK,
		Message: "No stale wisp activity detected",
	}
}

// Helper functions

// discoverRigs finds all registered rigs (shared helper).
func discoverRigs(townRoot string) ([]string, error) {
	rigsPath := filepath.Join(townRoot, "mayor", "rigs.json")
	data, err := os.ReadFile(rigsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var rigsConfig config.RigsConfig
	if err := json.Unmarshal(data, &rigsConfig); err != nil {
		return nil, err
	}

	var rigs []string
	for name := range rigsConfig.Rigs {
		rigs = append(rigs, name)
	}
	return rigs, nil
}

// dirSize calculates the total size of a directory.
func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// formatSize formats bytes as human-readable size.
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}

// formatDuration formats a duration as human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0f seconds", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1f hours", d.Hours())
	}
	return fmt.Sprintf("%.1f days", d.Hours()/24)
}
