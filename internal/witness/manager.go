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
			m.saveState(w)
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
			proc.Signal(os.Interrupt)
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

	for {
		select {
		case <-ticker.C:
			m.checkAndProcess(w)
		}
	}
}

// checkAndProcess performs health check and processes shutdown requests.
func (m *Manager) checkAndProcess(w *Witness) {
	// Perform health check
	if err := m.healthCheck(w); err != nil {
		fmt.Printf("Health check error: %v\n", err)
	}

	// Check for shutdown requests
	if err := m.processShutdownRequests(w); err != nil {
		fmt.Printf("Shutdown request error: %v\n", err)
	}
}

// healthCheck performs a health check on all monitored polecats.
func (m *Manager) healthCheck(w *Witness) error {
	now := time.Now()
	w.LastCheckAt = &now
	w.Stats.TotalChecks++
	w.Stats.TodayChecks++

	return m.saveState(w)
}

// processShutdownRequests checks mail for lifecycle requests and handles them.
func (m *Manager) processShutdownRequests(w *Witness) error {
	// Get witness mailbox via bd mail inbox
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

// WitnessMessage represents a mail message for the witness.
type WitnessMessage struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	From    string `json:"from"`
}

// getWitnessMessages retrieves unread messages for the witness.
func (m *Manager) getWitnessMessages() ([]WitnessMessage, error) {
	// Use bd mail inbox --json
	cmd := exec.Command("bd", "mail", "inbox", "--json")
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
	cmd.Run() // Ignore errors
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

// cleanupPolecat performs the full cleanup sequence for an ephemeral polecat.
// 1. Kill session
// 2. Remove worktree
// 3. Delete branch
func (m *Manager) cleanupPolecat(polecatName string) error {
	fmt.Printf("  Cleaning up polecat %s...\n", polecatName)

	// Get managers
	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, m.rig)
	polecatGit := git.NewGit(m.rig.Path)
	polecatMgr := polecat.NewManager(m.rig, polecatGit)

	// 1. Kill session
	running, err := sessMgr.IsRunning(polecatName)
	if err == nil && running {
		fmt.Printf("    Killing session...\n")
		if err := sessMgr.Stop(polecatName, true); err != nil {
			fmt.Printf("    Warning: failed to stop session: %v\n", err)
		}
	}

	// 2. Remove worktree (this also removes the directory)
	fmt.Printf("    Removing worktree...\n")
	if err := polecatMgr.Remove(polecatName, true); err != nil {
		// Only error if polecat actually exists
		if !errors.Is(err, polecat.ErrPolecatNotFound) {
			return fmt.Errorf("removing worktree: %w", err)
		}
	}

	// 3. Delete branch from mayor's clone
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
