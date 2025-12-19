package polecat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/rig"
)

// Common errors
var (
	ErrPolecatExists   = errors.New("polecat already exists")
	ErrPolecatNotFound = errors.New("polecat not found")
	ErrHasChanges      = errors.New("polecat has uncommitted changes")
)

// Manager handles polecat lifecycle.
type Manager struct {
	rig *rig.Rig
	git *git.Git
}

// NewManager creates a new polecat manager.
func NewManager(r *rig.Rig, g *git.Git) *Manager {
	return &Manager{
		rig: r,
		git: g,
	}
}

// polecatDir returns the directory for a polecat.
func (m *Manager) polecatDir(name string) string {
	return filepath.Join(m.rig.Path, "polecats", name)
}

// stateFile returns the state file path for a polecat.
func (m *Manager) stateFile(name string) string {
	return filepath.Join(m.polecatDir(name), "state.json")
}

// exists checks if a polecat exists.
func (m *Manager) exists(name string) bool {
	_, err := os.Stat(m.polecatDir(name))
	return err == nil
}

// Add creates a new polecat as a git worktree from the refinery clone.
// This is much faster than a full clone and shares objects with the refinery.
func (m *Manager) Add(name string) (*Polecat, error) {
	if m.exists(name) {
		return nil, ErrPolecatExists
	}

	polecatPath := m.polecatDir(name)
	branchName := fmt.Sprintf("polecat/%s", name)

	// Create polecats directory if needed
	polecatsDir := filepath.Join(m.rig.Path, "polecats")
	if err := os.MkdirAll(polecatsDir, 0755); err != nil {
		return nil, fmt.Errorf("creating polecats dir: %w", err)
	}

	// Use Mayor's clone as the base for worktrees (Mayor is canonical for the rig)
	mayorPath := filepath.Join(m.rig.Path, "mayor", "rig")
	mayorGit := git.NewGit(mayorPath)

	// Verify Mayor's clone exists
	if _, err := os.Stat(mayorPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("mayor clone not found at %s (run 'gt rig add' to set up rig structure)", mayorPath)
	}

	// Create worktree with new branch
	// git worktree add -b polecat/<name> <path>
	if err := mayorGit.WorktreeAdd(polecatPath, branchName); err != nil {
		return nil, fmt.Errorf("creating worktree: %w", err)
	}

	// Create polecat state - ephemeral polecats start in working state
	now := time.Now()
	polecat := &Polecat{
		Name:      name,
		Rig:       m.rig.Name,
		State:     StateWorking,
		ClonePath: polecatPath,
		Branch:    branchName,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Save state
	if err := m.saveState(polecat); err != nil {
		// Clean up worktree on failure
		mayorGit.WorktreeRemove(polecatPath, true)
		return nil, fmt.Errorf("saving state: %w", err)
	}

	return polecat, nil
}

// Remove deletes a polecat worktree.
// If force is true, removes even with uncommitted changes.
func (m *Manager) Remove(name string, force bool) error {
	if !m.exists(name) {
		return ErrPolecatNotFound
	}

	polecatPath := m.polecatDir(name)
	polecatGit := git.NewGit(polecatPath)

	// Check for uncommitted changes unless force
	if !force {
		hasChanges, err := polecatGit.HasUncommittedChanges()
		if err == nil && hasChanges {
			return ErrHasChanges
		}
	}

	// Use Mayor's clone to remove the worktree properly
	mayorPath := filepath.Join(m.rig.Path, "mayor", "rig")
	mayorGit := git.NewGit(mayorPath)

	// Try to remove as a worktree first (use force flag for worktree removal too)
	if err := mayorGit.WorktreeRemove(polecatPath, force); err != nil {
		// Fall back to direct removal if worktree removal fails
		// (e.g., if this is an old-style clone, not a worktree)
		if removeErr := os.RemoveAll(polecatPath); removeErr != nil {
			return fmt.Errorf("removing polecat dir: %w", removeErr)
		}
	}

	// Prune any stale worktree entries
	mayorGit.WorktreePrune()

	return nil
}

// List returns all polecats in the rig.
func (m *Manager) List() ([]*Polecat, error) {
	polecatsDir := filepath.Join(m.rig.Path, "polecats")

	entries, err := os.ReadDir(polecatsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading polecats dir: %w", err)
	}

	var polecats []*Polecat
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		polecat, err := m.Get(entry.Name())
		if err != nil {
			continue // Skip invalid polecats
		}
		polecats = append(polecats, polecat)
	}

	return polecats, nil
}

// Get returns a specific polecat by name.
func (m *Manager) Get(name string) (*Polecat, error) {
	if !m.exists(name) {
		return nil, ErrPolecatNotFound
	}

	return m.loadState(name)
}

// SetState updates a polecat's state.
func (m *Manager) SetState(name string, state State) error {
	polecat, err := m.Get(name)
	if err != nil {
		return err
	}

	polecat.State = state
	polecat.UpdatedAt = time.Now()

	return m.saveState(polecat)
}

// AssignIssue assigns an issue to a polecat.
func (m *Manager) AssignIssue(name, issue string) error {
	polecat, err := m.Get(name)
	if err != nil {
		return err
	}

	polecat.Issue = issue
	polecat.State = StateWorking
	polecat.UpdatedAt = time.Now()

	return m.saveState(polecat)
}

// ClearIssue removes the issue assignment from a polecat.
// In the ephemeral model, this transitions to Done state for cleanup.
func (m *Manager) ClearIssue(name string) error {
	polecat, err := m.Get(name)
	if err != nil {
		return err
	}

	polecat.Issue = ""
	polecat.State = StateDone
	polecat.UpdatedAt = time.Now()

	return m.saveState(polecat)
}

// Wake transitions a polecat from idle to active.
// Deprecated: In the ephemeral model, polecats start in working state.
// This method is kept for backward compatibility with existing polecats.
func (m *Manager) Wake(name string) error {
	polecat, err := m.Get(name)
	if err != nil {
		return err
	}

	// Accept both idle and done states for legacy compatibility
	if polecat.State != StateIdle && polecat.State != StateDone {
		return fmt.Errorf("polecat is not idle (state: %s)", polecat.State)
	}

	return m.SetState(name, StateWorking)
}

// Sleep transitions a polecat from active to idle.
// Deprecated: In the ephemeral model, polecats are deleted when done.
// This method is kept for backward compatibility.
func (m *Manager) Sleep(name string) error {
	polecat, err := m.Get(name)
	if err != nil {
		return err
	}

	// Accept working state as well for legacy compatibility
	if polecat.State != StateActive && polecat.State != StateWorking {
		return fmt.Errorf("polecat is not active (state: %s)", polecat.State)
	}

	return m.SetState(name, StateDone)
}

// Finish transitions a polecat from working/done/stuck to idle and clears the issue.
func (m *Manager) Finish(name string) error {
	polecat, err := m.Get(name)
	if err != nil {
		return err
	}

	// Only allow finishing from working-related states
	switch polecat.State {
	case StateWorking, StateDone, StateStuck:
		// OK to finish
	default:
		return fmt.Errorf("polecat is not in a finishing state (state: %s)", polecat.State)
	}

	polecat.Issue = ""
	polecat.State = StateIdle
	polecat.UpdatedAt = time.Now()

	return m.saveState(polecat)
}

// Reset forces a polecat to idle state regardless of current state.
func (m *Manager) Reset(name string) error {
	polecat, err := m.Get(name)
	if err != nil {
		return err
	}

	polecat.Issue = ""
	polecat.State = StateIdle
	polecat.UpdatedAt = time.Now()

	return m.saveState(polecat)
}

// saveState persists polecat state to disk.
func (m *Manager) saveState(polecat *Polecat) error {
	data, err := json.MarshalIndent(polecat, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	stateFile := m.stateFile(polecat.Name)
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}

	return nil
}

// loadState reads polecat state from disk.
func (m *Manager) loadState(name string) (*Polecat, error) {
	stateFile := m.stateFile(name)

	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			// Return minimal polecat if state file missing
			// Use StateWorking since ephemeral polecats are always working
			return &Polecat{
				Name:      name,
				Rig:       m.rig.Name,
				State:     StateWorking,
				ClonePath: m.polecatDir(name),
			}, nil
		}
		return nil, fmt.Errorf("reading state: %w", err)
	}

	var polecat Polecat
	if err := json.Unmarshal(data, &polecat); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}

	return &polecat, nil
}
