package witness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
)

// Common errors
var (
	ErrNotRunning     = errors.New("witness not running")
	ErrAlreadyRunning = errors.New("witness already running")
)

// Manager handles witness lifecycle and monitoring operations.
type Manager struct {
	rig     *rig.Rig
	workDir string
}

// NewManager creates a new witness manager for a rig.
func NewManager(r *rig.Rig) *Manager {
	return &Manager{
		rig:     r,
		workDir: r.Path,
	}
}

// stateFile returns the path to the witness state file.
func (m *Manager) stateFile() string {
	return filepath.Join(m.rig.Path, ".gastown", "witness.json")
}

// loadState loads witness state from disk.
func (m *Manager) loadState() (*Witness, error) {
	data, err := os.ReadFile(m.stateFile())
	if err != nil {
		if os.IsNotExist(err) {
			return &Witness{
				RigName: m.rig.Name,
				State:   StateStopped,
			}, nil
		}
		return nil, err
	}

	var w Witness
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, err
	}

	return &w, nil
}

// saveState persists witness state to disk.
func (m *Manager) saveState(w *Witness) error {
	dir := filepath.Dir(m.stateFile())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.stateFile(), data, 0644)
}

// Status returns the current witness status.
func (m *Manager) Status() (*Witness, error) {
	w, err := m.loadState()
	if err != nil {
		return nil, err
	}

	// If running, verify process is still alive
	if w.State == StateRunning && w.PID > 0 {
		if !processExists(w.PID) {
			w.State = StateStopped
			w.PID = 0
			_ = m.saveState(w)
		}
	}

	// Update monitored polecats list
	w.MonitoredPolecats = m.rig.Polecats

	return w, nil
}

// Start starts the witness.
// If foreground is true, runs in the current process (blocking).
// Otherwise, spawns a background process.
func (m *Manager) Start(foreground bool) error {
	w, err := m.loadState()
	if err != nil {
		return err
	}

	if w.State == StateRunning && w.PID > 0 && processExists(w.PID) {
		return ErrAlreadyRunning
	}

	now := time.Now()
	w.State = StateRunning
	w.StartedAt = &now
	w.PID = os.Getpid() // For foreground mode; background would set actual PID
	w.MonitoredPolecats = m.rig.Polecats

	if err := m.saveState(w); err != nil {
		return err
	}

	if foreground {
		// Run the monitoring loop (blocking)
		return m.run(w)
	}

	// Background mode: spawn a new process
	// For MVP, we just mark as running - actual daemon implementation later
	return nil
}

// Stop stops the witness.
func (m *Manager) Stop() error {
	w, err := m.loadState()
	if err != nil {
		return err
	}

	if w.State != StateRunning {
		return ErrNotRunning
	}

	// If we have a PID, try to stop it gracefully
	if w.PID > 0 && w.PID != os.Getpid() {
		// Send SIGTERM
		if proc, err := os.FindProcess(w.PID); err == nil {
			_ = proc.Signal(os.Interrupt)
		}
	}

	w.State = StateStopped
	w.PID = 0

	return m.saveState(w)
}

// run is the main monitoring loop (for foreground mode).
func (m *Manager) run(w *Witness) error {
	fmt.Println("Witness running...")
	fmt.Println("Press Ctrl+C to stop")

	// Initial check immediately
	m.checkAndProcess(w)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m.checkAndProcess(w)
	}
	return nil
}

// checkAndProcess performs health check, shutdown processing, and auto-spawn.
func (m *Manager) checkAndProcess(w *Witness) {
	// Perform health check
	if err := m.healthCheck(w); err != nil {
		fmt.Printf("Health check error: %v\n", err)
	}

	// Check for shutdown requests
	if err := m.processShutdownRequests(w); err != nil {
		fmt.Printf("Shutdown request error: %v\n", err)
	}

	// Auto-spawn for ready work (if enabled)
	if w.Config.AutoSpawn {
		if err := m.autoSpawnForReadyWork(w); err != nil {
			fmt.Printf("Auto-spawn error: %v\n", err)
		}
	}
}

// healthCheck performs a health check on all monitored polecats.
func (m *Manager) healthCheck(w *Witness) error {
	now := time.Now()
	w.LastCheckAt = &now
	w.Stats.TotalChecks++
	w.Stats.TodayChecks++

	// List polecats
	polecatMgr := polecat.NewManager(m.rig, git.NewGit(m.rig.Path))
	polecats, err := polecatMgr.List()
	if err != nil {
		return fmt.Errorf("listing polecats: %w", err)
	}

	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, m.rig)

	// Update monitored polecats list
	var active []string
	for _, p := range polecats {
		running, _ := sessMgr.IsRunning(p.Name)
		if running {
			active = append(active, p.Name)

			// Check health of each active polecat
			status := m.checkPolecatHealth(p.Name, p.ClonePath)
			if status == PolecatStuck {
				m.handleStuckPolecat(w, p.Name)
			}
		}
	}
	w.MonitoredPolecats = active

	return m.saveState(w)
}

// PolecatHealthStatus represents the health status of a polecat.
type PolecatHealthStatus int

const (
	// PolecatHealthy means the polecat is working normally.
	PolecatHealthy PolecatHealthStatus = iota
	// PolecatStuck means the polecat has no recent activity.
	PolecatStuck
	// PolecatDead means the polecat session is not responding.
	PolecatDead
)

// StuckThresholdMinutes is the default time without activity before a polecat is considered stuck.
const StuckThresholdMinutes = 30

// checkPolecatHealth checks if a polecat is healthy based on recent activity.
func (m *Manager) checkPolecatHealth(name, path string) PolecatHealthStatus {
	threshold := time.Duration(StuckThresholdMinutes) * time.Minute

	// Check 1: Git activity (most reliable indicator of work)
	gitPath := filepath.Join(path, ".git")
	if info, err := os.Stat(gitPath); err == nil {
		if time.Since(info.ModTime()) < threshold {
			return PolecatHealthy
		}
	}

	// Check 2: State file activity
	stateFile := filepath.Join(path, ".gastown", "state.json")
	if info, err := os.Stat(stateFile); err == nil {
		if time.Since(info.ModTime()) < threshold {
			return PolecatHealthy
		}
	}

	// Check 3: Any file modification in the polecat directory
	latestMod := m.getLatestModTime(path)
	if !latestMod.IsZero() && time.Since(latestMod) < threshold {
		return PolecatHealthy
	}

	return PolecatStuck
}

// getLatestModTime finds the most recent modification time in a directory.
func (m *Manager) getLatestModTime(dir string) time.Time {
	var latest time.Time

	// Quick check: just look at a few key locations
	locations := []string{
		filepath.Join(dir, ".git", "logs", "HEAD"),
		filepath.Join(dir, ".git", "index"),
		filepath.Join(dir, ".beads", "issues.jsonl"),
	}

	for _, loc := range locations {
		if info, err := os.Stat(loc); err == nil {
			if info.ModTime().After(latest) {
				latest = info.ModTime()
			}
		}
	}

	return latest
}

// handleStuckPolecat handles a polecat that appears to be stuck.
func (m *Manager) handleStuckPolecat(w *Witness, polecatName string) {
	fmt.Printf("Polecat %s appears stuck (no activity for %d minutes)\n",
		polecatName, StuckThresholdMinutes)

	// Check nudge history for this polecat
	nudgeCount := m.getNudgeCount(w, polecatName)

	if nudgeCount == 0 {
		// First stuck detection: send a nudge
		fmt.Printf("  Sending nudge to %s...\n", polecatName)
		if err := m.sendNudge(polecatName, "No activity detected. Are you still working?"); err != nil {
			fmt.Printf("  Warning: failed to send nudge: %v\n", err)
		}
		m.recordNudge(w, polecatName)
		w.Stats.TotalNudges++
		w.Stats.TodayNudges++
	} else if nudgeCount == 1 {
		// Second stuck detection: escalate to Mayor
		fmt.Printf("  Escalating %s to Mayor (no response to nudge)...\n", polecatName)
		if err := m.escalateToMayor(polecatName); err != nil {
			fmt.Printf("  Warning: failed to escalate: %v\n", err)
		}
		w.Stats.TotalEscalations++
		m.recordNudge(w, polecatName)
	} else {
		// Third+ stuck detection: log but wait for human confirmation
		fmt.Printf("  %s still stuck (waiting for human intervention)\n", polecatName)
	}
}

// getNudgeCount returns how many times a polecat has been nudged.
func (m *Manager) getNudgeCount(w *Witness, polecatName string) int {
	// Count occurrences in SpawnedIssues that start with "nudge:" prefix
	// We reuse SpawnedIssues to track nudges with a "nudge:<name>" pattern
	count := 0
	nudgeKey := "nudge:" + polecatName
	for _, entry := range w.SpawnedIssues {
		if entry == nudgeKey {
			count++
		}
	}
	return count
}

// recordNudge records that a nudge was sent to a polecat.
func (m *Manager) recordNudge(w *Witness, polecatName string) {
	nudgeKey := "nudge:" + polecatName
	w.SpawnedIssues = append(w.SpawnedIssues, nudgeKey)
}

// escalateToMayor sends an escalation message to the Mayor.
func (m *Manager) escalateToMayor(polecatName string) error {
	subject := fmt.Sprintf("ESCALATION: Polecat %s stuck", polecatName)
	body := fmt.Sprintf(`Polecat %s in rig %s appears stuck.

This polecat has been unresponsive for over %d minutes despite nudging.

Recommended actions:
1. Check 'gt session attach %s/%s' to see current state
2. If truly stuck, run 'gt session stop %s/%s' to kill the session
3. Investigate root cause

Rig: %s
Time: %s
`, polecatName, m.rig.Name, StuckThresholdMinutes*2,
		m.rig.Name, polecatName,
		m.rig.Name, polecatName,
		m.rig.Name, time.Now().Format(time.RFC3339))

	cmd := exec.Command("bd", "mail", "send", "mayor/",
		"-s", subject,
		"-m", body,
	)
	cmd.Dir = m.workDir

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}

	return nil
}

// processShutdownRequests checks mail for lifecycle requests and handles them.
func (m *Manager) processShutdownRequests(w *Witness) error {
	// Get witness mailbox via gt mail
	messages, err := m.getWitnessMessages()
	if err != nil {
		return fmt.Errorf("getting messages: %w", err)
	}

	for _, msg := range messages {
		// Look for LIFECYCLE requests
		if strings.Contains(msg.Subject, "LIFECYCLE:") && strings.Contains(msg.Subject, "shutdown") {
			fmt.Printf("Processing shutdown request: %s\n", msg.Subject)

			// Extract polecat name from message body
			polecatName := extractPolecatName(msg.Body)
			if polecatName == "" {
				fmt.Printf("  Warning: could not extract polecat name from message\n")
				m.ackMessage(msg.ID)
				continue
			}

			fmt.Printf("  Polecat: %s\n", polecatName)

			// Verify polecat state before cleanup
			if err := m.verifyPolecatState(polecatName); err != nil {
				fmt.Printf("  Verification failed: %v\n", err)

				// Send nudge to polecat
				if err := m.sendNudge(polecatName, err.Error()); err != nil {
					fmt.Printf("  Warning: failed to send nudge: %v\n", err)
				}

				// Don't ack message - will retry on next check
				continue
			}

			// Perform cleanup
			if err := m.cleanupPolecat(polecatName); err != nil {
				fmt.Printf("  Cleanup error: %v\n", err)
				// Don't ack message on error - will retry
				continue
			}

			fmt.Printf("  Cleanup complete\n")

			// Acknowledge the message
			m.ackMessage(msg.ID)
		}
	}

	return nil
}

// verifyPolecatState checks that a polecat is safe to clean up.
func (m *Manager) verifyPolecatState(polecatName string) error {
	polecatPath := filepath.Join(m.rig.Path, "polecats", polecatName)

	// Check if polecat directory exists
	if _, err := os.Stat(polecatPath); os.IsNotExist(err) {
		// Already cleaned up, that's fine
		return nil
	}

	// 1. Check git status is clean
	polecatGit := git.NewGit(polecatPath)
	status, err := polecatGit.Status()
	if err != nil {
		return fmt.Errorf("checking git status: %w", err)
	}
	if !status.Clean {
		return fmt.Errorf("git working tree is not clean")
	}

	// Note: beads changes would be reflected in git status above,
	// since beads files are tracked in git.

	// Note: MR submission is now done automatically by polecat's handoff command,
	// so we don't need to verify it here - the polecat wouldn't have requested
	// shutdown if that step failed

	return nil
}

// sendNudge sends a message to a polecat asking it to fix its state.
func (m *Manager) sendNudge(polecatName, reason string) error {
	subject := fmt.Sprintf("NUDGE: Cannot shutdown - %s", reason)
	body := fmt.Sprintf(`Your shutdown request was denied because: %s

Please fix the issue and run 'gt handoff' again.

Polecat: %s
Rig: %s
Time: %s
`, reason, polecatName, m.rig.Name, time.Now().Format(time.RFC3339))

	// Send via gt mail
	recipient := fmt.Sprintf("%s/%s", m.rig.Name, polecatName)
	cmd := exec.Command("gt", "mail", "send", recipient,
		"-s", subject,
		"-m", body,
	)
	cmd.Dir = m.workDir

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}

	return nil
}

// WitnessMessage represents a mail message for the witness.
type WitnessMessage struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	From    string `json:"from"`
}

// getWitnessMessages retrieves unread messages for the witness.
func (m *Manager) getWitnessMessages() ([]WitnessMessage, error) {
	// Use gt mail inbox --json
	cmd := exec.Command("gt", "mail", "inbox", "--json")
	cmd.Dir = m.workDir
	cmd.Env = append(os.Environ(), "BEADS_AGENT_NAME="+m.rig.Name+"-witness")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// No messages is not an error
		if strings.Contains(stderr.String(), "no messages") {
			return nil, nil
		}
		return nil, fmt.Errorf("%s", stderr.String())
	}

	if stdout.Len() == 0 {
		return nil, nil
	}

	var messages []WitnessMessage
	if err := json.Unmarshal(stdout.Bytes(), &messages); err != nil {
		// Try parsing as empty array
		if strings.TrimSpace(stdout.String()) == "[]" {
			return nil, nil
		}
		return nil, fmt.Errorf("parsing messages: %w", err)
	}

	return messages, nil
}

// ackMessage acknowledges a message (marks it as read/handled).
func (m *Manager) ackMessage(id string) {
	cmd := exec.Command("bd", "mail", "ack", id)
	cmd.Dir = m.workDir
	_ = cmd.Run() // Ignore errors
}

// extractPolecatName extracts the polecat name from a lifecycle request body.
func extractPolecatName(body string) string {
	// Look for "Polecat: <name>" pattern
	re := regexp.MustCompile(`Polecat:\s*(\S+)`)
	matches := re.FindStringSubmatch(body)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// cleanupPolecat performs the full cleanup sequence for a transient polecat.
// 1. Check for uncommitted work (stubbornly refuses to lose work)
// 2. Kill session
// 3. Remove worktree
// 4. Delete branch
//
// If the polecat has uncommitted work (changes, stashes, or unpushed commits),
// the cleanup is aborted and an error is returned. The Witness will retry later.
func (m *Manager) cleanupPolecat(polecatName string) error {
	fmt.Printf("  Cleaning up polecat %s...\n", polecatName)

	// Get managers
	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, m.rig)
	polecatGit := git.NewGit(m.rig.Path)
	polecatMgr := polecat.NewManager(m.rig, polecatGit)

	// Get polecat path for git check
	polecatPath := filepath.Join(m.rig.Path, "polecats", polecatName)

	// 1. Check for uncommitted work BEFORE doing anything destructive
	pGit := git.NewGit(polecatPath)
	status, err := pGit.CheckUncommittedWork()
	if err != nil {
		// If we can't check (e.g., not a git repo), log warning but continue
		fmt.Printf("    Warning: could not check uncommitted work: %v\n", err)
	} else if !status.Clean() {
		// REFUSE to clean up - this is the key safety feature
		fmt.Printf("    REFUSING to cleanup - polecat has uncommitted work:\n")
		if status.HasUncommittedChanges {
			fmt.Printf("      • %d uncommitted change(s)\n", len(status.ModifiedFiles)+len(status.UntrackedFiles))
		}
		if status.StashCount > 0 {
			fmt.Printf("      • %d stash(es)\n", status.StashCount)
		}
		if status.UnpushedCommits > 0 {
			fmt.Printf("      • %d unpushed commit(s)\n", status.UnpushedCommits)
		}
		return fmt.Errorf("polecat %s has uncommitted work: %s", polecatName, status.String())
	}

	// 2. Kill session
	running, err := sessMgr.IsRunning(polecatName)
	if err == nil && running {
		fmt.Printf("    Killing session...\n")
		if err := sessMgr.Stop(polecatName, true); err != nil {
			fmt.Printf("    Warning: failed to stop session: %v\n", err)
		}
	}

	// 3. Remove worktree (this also removes the directory)
	// Use force=true since we've already verified no uncommitted work
	fmt.Printf("    Removing worktree...\n")
	if err := polecatMgr.RemoveWithOptions(polecatName, true, true); err != nil {
		// Only error if polecat actually exists
		if !errors.Is(err, polecat.ErrPolecatNotFound) {
			return fmt.Errorf("removing worktree: %w", err)
		}
	}

	// 4. Delete branch from mayor's clone
	branchName := fmt.Sprintf("polecat/%s", polecatName)
	mayorPath := filepath.Join(m.rig.Path, "mayor", "rig")
	mayorGit := git.NewGit(mayorPath)

	fmt.Printf("    Deleting branch %s...\n", branchName)
	if err := mayorGit.DeleteBranch(branchName, true); err != nil {
		// Branch might already be deleted or merged, not a critical error
		fmt.Printf("    Warning: failed to delete branch: %v\n", err)
	}

	return nil
}

// processExists checks if a process with the given PID exists.
func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; signal 0 tests existence
	err = proc.Signal(nil)
	return err == nil
}

// ReadyIssue represents an issue from bd ready --json output.
type ReadyIssue struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Type   string `json:"issue_type"`
	Status string `json:"status"`
}

// autoSpawnForReadyWork spawns polecats for ready work up to capacity.
func (m *Manager) autoSpawnForReadyWork(w *Witness) error {
	// Get current active polecat count
	activeCount, err := m.getActivePolecatCount()
	if err != nil {
		return fmt.Errorf("counting polecats: %w", err)
	}

	maxWorkers := w.Config.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 4 // Default
	}

	if activeCount >= maxWorkers {
		// At capacity, nothing to do
		return nil
	}

	// Get ready issues
	issues, err := m.getReadyIssues()
	if err != nil {
		return fmt.Errorf("getting ready issues: %w", err)
	}

	// Filter issues (exclude merge-requests, epics, and already-spawned issues)
	var spawnableIssues []ReadyIssue
	for _, issue := range issues {
		// Skip merge-requests and epics
		if issue.Type == "merge-request" || issue.Type == "epic" {
			continue
		}

		// Skip if already spawned
		if m.isAlreadySpawned(w, issue.ID) {
			continue
		}

		// Filter by epic if configured
		if w.Config.EpicID != "" {
			isChild, err := m.isChildOfEpic(issue.ID, w.Config.EpicID)
			if err != nil {
				// Skip issues we can't verify - safer than including unknown work
				continue
			}
			if !isChild {
				continue
			}
		}

		// Filter by prefix if configured
		if w.Config.IssuePrefix != "" {
			if !strings.HasPrefix(issue.ID, w.Config.IssuePrefix) {
				continue
			}
		}

		spawnableIssues = append(spawnableIssues, issue)
	}

	// Spawn up to capacity
	spawnDelay := w.Config.SpawnDelayMs
	if spawnDelay <= 0 {
		spawnDelay = 5000 // Default 5 seconds
	}

	spawned := 0
	for _, issue := range spawnableIssues {
		if activeCount+spawned >= maxWorkers {
			break
		}

		fmt.Printf("Auto-spawning for issue %s: %s\n", issue.ID, issue.Title)

		if err := m.spawnPolecat(issue.ID); err != nil {
			fmt.Printf("  Spawn failed: %v\n", err)
			continue
		}

		// Track that we spawned for this issue
		w.SpawnedIssues = append(w.SpawnedIssues, issue.ID)
		spawned++

		// Delay between spawns
		if spawned < len(spawnableIssues) && activeCount+spawned < maxWorkers {
			time.Sleep(time.Duration(spawnDelay) * time.Millisecond)
		}
	}

	if spawned > 0 {
		// Save state to persist spawned issues list
		return m.saveState(w)
	}

	return nil
}

// getActivePolecatCount returns the number of polecats with active tmux sessions.
func (m *Manager) getActivePolecatCount() (int, error) {
	polecatMgr := polecat.NewManager(m.rig, git.NewGit(m.rig.Path))
	polecats, err := polecatMgr.List()
	if err != nil {
		return 0, err
	}

	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, m.rig)

	count := 0
	for _, p := range polecats {
		running, _ := sessMgr.IsRunning(p.Name)
		if running {
			count++
		}
	}

	return count, nil
}

// getReadyIssues returns issues ready to work (no blockers).
func (m *Manager) getReadyIssues() ([]ReadyIssue, error) {
	cmd := exec.Command("bd", "ready", "--json")
	cmd.Dir = m.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s", stderr.String())
	}

	if stdout.Len() == 0 {
		return nil, nil
	}

	var issues []ReadyIssue
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		return nil, fmt.Errorf("parsing ready issues: %w", err)
	}

	return issues, nil
}

// issueDependency represents a dependency from bd show --json output.
type issueDependency struct {
	ID             string `json:"id"`
	DependencyType string `json:"dependency_type"`
}

// issueWithDeps represents an issue with its dependencies from bd show --json.
type issueWithDeps struct {
	ID         string            `json:"id"`
	Dependents []issueDependency `json:"dependents"`
}

// isChildOfEpic checks if an issue blocks (is a child of) the given epic.
func (m *Manager) isChildOfEpic(issueID, epicID string) (bool, error) {
	cmd := exec.Command("bd", "show", issueID, "--json")
	cmd.Dir = m.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("%s", stderr.String())
	}

	var issues []issueWithDeps
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		return false, fmt.Errorf("parsing issue: %w", err)
	}

	if len(issues) == 0 {
		return false, nil
	}

	// Check if the epic is in the dependents with type "blocks"
	for _, dep := range issues[0].Dependents {
		if dep.ID == epicID && dep.DependencyType == "blocks" {
			return true, nil
		}
	}

	return false, nil
}

// isAlreadySpawned checks if an issue has already been spawned.
func (m *Manager) isAlreadySpawned(w *Witness, issueID string) bool {
	for _, id := range w.SpawnedIssues {
		if id == issueID {
			return true
		}
	}
	return false
}

// spawnPolecat spawns a polecat for an issue using gt spawn.
func (m *Manager) spawnPolecat(issueID string) error {
	cmd := exec.Command("gt", "spawn", "--rig", m.rig.Name, "--issue", issueID)
	cmd.Dir = m.workDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}

	fmt.Printf("  Spawned: %s\n", strings.TrimSpace(string(output)))
	return nil
}
