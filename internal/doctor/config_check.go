package doctor

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/constants"
)

// SettingsCheck verifies each rig has a settings/ directory.
type SettingsCheck struct {
	FixableCheck
	missingSettings []string // Cached during Run for use in Fix
}

// NewSettingsCheck creates a new settings directory check.
func NewSettingsCheck() *SettingsCheck {
	return &SettingsCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "rig-settings",
				CheckDescription: "Check that rigs have settings/ directory",
			},
		},
	}
}

// Run checks if all rigs have a settings/ directory.
func (c *SettingsCheck) Run(ctx *CheckContext) *CheckResult {
	rigs := c.findRigs(ctx.TownRoot)
	if len(rigs) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No rigs found",
		}
	}

	var missing []string
	var ok int

	for _, rig := range rigs {
		settingsPath := constants.RigSettingsPath(rig)
		if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
			relPath, _ := filepath.Rel(ctx.TownRoot, rig)
			missing = append(missing, relPath)
		} else {
			ok++
		}
	}

	// Cache for Fix
	c.missingSettings = nil
	for _, rig := range rigs {
		settingsPath := constants.RigSettingsPath(rig)
		if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
			c.missingSettings = append(c.missingSettings, settingsPath)
		}
	}

	if len(missing) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: fmt.Sprintf("All %d rig(s) have settings/ directory", ok),
		}
	}

	details := make([]string, len(missing))
	for i, m := range missing {
		details[i] = fmt.Sprintf("Missing: %s/settings/", m)
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d rig(s) missing settings/ directory", len(missing)),
		Details: details,
		FixHint: "Run 'gt doctor --fix' to create missing directories",
	}
}

// Fix creates missing settings/ directories.
func (c *SettingsCheck) Fix(ctx *CheckContext) error {
	for _, path := range c.missingSettings {
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", path, err)
		}
	}
	return nil
}

// RuntimeGitignoreCheck verifies .runtime/ is gitignored at town and rig levels.
type RuntimeGitignoreCheck struct {
	BaseCheck
}

// NewRuntimeGitignoreCheck creates a new runtime gitignore check.
func NewRuntimeGitignoreCheck() *RuntimeGitignoreCheck {
	return &RuntimeGitignoreCheck{
		BaseCheck: BaseCheck{
			CheckName:        "runtime-gitignore",
			CheckDescription: "Check that .runtime/ directories are gitignored",
		},
	}
}

// Run checks if .runtime/ is properly gitignored.
func (c *RuntimeGitignoreCheck) Run(ctx *CheckContext) *CheckResult {
	var issues []string

	// Check town-level .gitignore
	townGitignore := filepath.Join(ctx.TownRoot, ".gitignore")
	if !c.containsPattern(townGitignore, ".runtime") {
		issues = append(issues, "Town .gitignore missing .runtime/ pattern")
	}

	// Check each rig's .gitignore (in their git clones)
	rigs := c.findRigs(ctx.TownRoot)
	for _, rig := range rigs {
		// Check crew members
		crewPath := filepath.Join(rig, "crew")
		if crewEntries, err := os.ReadDir(crewPath); err == nil {
			for _, crew := range crewEntries {
				if crew.IsDir() && !strings.HasPrefix(crew.Name(), ".") {
					crewGitignore := filepath.Join(crewPath, crew.Name(), ".gitignore")
					if !c.containsPattern(crewGitignore, ".runtime") {
						relPath, _ := filepath.Rel(ctx.TownRoot, filepath.Join(crewPath, crew.Name()))
						issues = append(issues, fmt.Sprintf("%s .gitignore missing .runtime/ pattern", relPath))
					}
				}
			}
		}
	}

	if len(issues) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: ".runtime/ properly gitignored",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d location(s) missing .runtime gitignore", len(issues)),
		Details: issues,
		FixHint: "Add '.runtime/' to .gitignore files",
	}
}

// containsPattern checks if a gitignore file contains a pattern.
func (c *RuntimeGitignoreCheck) containsPattern(gitignorePath, pattern string) bool {
	file, err := os.Open(gitignorePath)
	if err != nil {
		return false // File doesn't exist
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Check for pattern match (with or without trailing slash, with or without glob prefix)
		// Accept: .runtime, .runtime/, /.runtime, /.runtime/, **/.runtime, **/.runtime/
		if line == pattern || line == pattern+"/" ||
			line == "/"+pattern || line == "/"+pattern+"/" ||
			line == "**/"+pattern || line == "**/"+pattern+"/" {
			return true
		}
	}
	return false
}

// findRigs returns rig directories within the town.
func (c *RuntimeGitignoreCheck) findRigs(townRoot string) []string {
	return findAllRigs(townRoot)
}

// LegacyGastownCheck warns if old .gastown/ directories still exist.
type LegacyGastownCheck struct {
	FixableCheck
	legacyDirs []string // Cached during Run for use in Fix
}

// NewLegacyGastownCheck creates a new legacy gastown check.
func NewLegacyGastownCheck() *LegacyGastownCheck {
	return &LegacyGastownCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "legacy-gastown",
				CheckDescription: "Check for old .gastown/ directories that should be migrated",
			},
		},
	}
}

// Run checks for legacy .gastown/ directories.
func (c *LegacyGastownCheck) Run(ctx *CheckContext) *CheckResult {
	var found []string

	// Check town-level .gastown/
	townGastown := filepath.Join(ctx.TownRoot, ".gastown")
	if info, err := os.Stat(townGastown); err == nil && info.IsDir() {
		found = append(found, ".gastown/ (town root)")
	}

	// Check each rig for .gastown/
	rigs := c.findRigs(ctx.TownRoot)
	for _, rig := range rigs {
		rigGastown := filepath.Join(rig, ".gastown")
		if info, err := os.Stat(rigGastown); err == nil && info.IsDir() {
			relPath, _ := filepath.Rel(ctx.TownRoot, rig)
			found = append(found, fmt.Sprintf("%s/.gastown/", relPath))
		}
	}

	// Cache for Fix
	c.legacyDirs = nil
	if info, err := os.Stat(townGastown); err == nil && info.IsDir() {
		c.legacyDirs = append(c.legacyDirs, townGastown)
	}
	for _, rig := range rigs {
		rigGastown := filepath.Join(rig, ".gastown")
		if info, err := os.Stat(rigGastown); err == nil && info.IsDir() {
			c.legacyDirs = append(c.legacyDirs, rigGastown)
		}
	}

	if len(found) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No legacy .gastown/ directories found",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d legacy .gastown/ directory(ies) found", len(found)),
		Details: found,
		FixHint: "Run 'gt doctor --fix' to remove after verifying migration is complete",
	}
}

// Fix removes legacy .gastown/ directories.
func (c *LegacyGastownCheck) Fix(ctx *CheckContext) error {
	for _, dir := range c.legacyDirs {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("failed to remove %s: %w", dir, err)
		}
	}
	return nil
}

// findRigs returns rig directories within the town.
func (c *LegacyGastownCheck) findRigs(townRoot string) []string {
	return findAllRigs(townRoot)
}

// findRigs returns rig directories within the town.
func (c *SettingsCheck) findRigs(townRoot string) []string {
	return findAllRigs(townRoot)
}

// findAllRigs is a shared helper that returns all rig directories within a town.
func findAllRigs(townRoot string) []string {
	var rigs []string

	entries, err := os.ReadDir(townRoot)
	if err != nil {
		return rigs
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Skip non-rig directories
		name := entry.Name()
		if name == "mayor" || name == ".beads" || strings.HasPrefix(name, ".") {
			continue
		}

		rigPath := filepath.Join(townRoot, name)

		// Check if this looks like a rig (has crew/, polecats/, witness/, or refinery/)
		markers := []string{"crew", "polecats", "witness", "refinery"}
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(rigPath, marker)); err == nil {
				rigs = append(rigs, rigPath)
				break
			}
		}
	}

	return rigs
}
