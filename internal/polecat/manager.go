package polecat

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/rig"
)

// Common errors
var (
	ErrPolecatExists     = errors.New("polecat already exists")
	ErrPolecatNotFound   = errors.New("polecat not found")
	ErrHasChanges        = errors.New("polecat has uncommitted changes")
	ErrHasUncommittedWork = errors.New("polecat has uncommitted work")
)

// UncommittedWorkError provides details about uncommitted work.
type UncommittedWorkError struct {
	PolecatName string
	Status      *git.UncommittedWorkStatus
}

func (e *UncommittedWorkError) Error() string {
	return fmt.Sprintf("polecat %s has uncommitted work: %s", e.PolecatName, e.Status.String())
}

func (e *UncommittedWorkError) Unwrap() error {
	return ErrHasUncommittedWork
}

// Manager handles polecat lifecycle.
type Manager struct {
	rig      *rig.Rig
	git      *git.Git
	beads    *beads.Beads
	namePool *NamePool
}

// NewManager creates a new polecat manager.
func NewManager(r *rig.Rig, g *git.Git) *Manager {
	// Use the rig root for beads operations (rig-level beads at .beads/)
	rigPath := r.Path

	// Try to load rig config for namepool settings
	rigConfigPath := filepath.Join(r.Path, ".gastown", "config.json")
	var pool *NamePool

	rigConfig, err := config.LoadRigConfig(rigConfigPath)
	if err == nil && rigConfig.Namepool != nil {
		// Use configured namepool settings
		pool = NewNamePoolWithConfig(
			r.Path,
			r.Name,
			rigConfig.Namepool.Style,
			rigConfig.Namepool.Names,
			rigConfig.Namepool.MaxBeforeNumbering,
		)
	} else {
		// Use defaults
		pool = NewNamePool(r.Path, r.Name)
	}
	_ = pool.Load() // Load existing state, ignore errors for new rigs

	return &Manager{
		rig:      r,
		git:      g,
		beads:    beads.New(rigPath),
		namePool: pool,
	}
}

// assigneeID returns the beads assignee identifier for a polecat.
// Format: "rig/polecatName" (e.g., "gastown/Toast")
func (m *Manager) assigneeID(name string) string {
	return fmt.Sprintf("%s/%s", m.rig.Name, name)
}

// polecatDir returns the directory for a polecat.
func (m *Manager) polecatDir(name string) string {
	return filepath.Join(m.rig.Path, "polecats", name)
}

// exists checks if a polecat exists.
func (m *Manager) exists(name string) bool {
	_, err := os.Stat(m.polecatDir(name))
	return err == nil
}

// Add creates a new polecat as a git worktree from the mayor's clone.
// This is much faster than a full clone and shares objects with the mayor.
// Polecat state is derived from beads assignee field, not state.json.
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

	// Check if branch already exists (e.g., from previous polecat that wasn't cleaned up)
	branchExists, err := mayorGit.BranchExists(branchName)
	if err != nil {
		return nil, fmt.Errorf("checking branch existence: %w", err)
	}

	// Create worktree - reuse existing branch if it exists
	if branchExists {
		// Branch exists, create worktree using existing branch
		if err := mayorGit.WorktreeAddExisting(polecatPath, branchName); err != nil {
			return nil, fmt.Errorf("creating worktree with existing branch: %w", err)
		}
	} else {
		// Create new branch with worktree
		// git worktree add -b polecat/<name> <path>
		if err := mayorGit.WorktreeAdd(polecatPath, branchName); err != nil {
			return nil, fmt.Errorf("creating worktree: %w", err)
		}
	}

	// Set up shared beads: polecat uses rig's .beads via redirect file.
	// This eliminates git sync overhead - all polecats share one database.
	if err := m.setupSharedBeads(polecatPath); err != nil {
		// Non-fatal - polecat can still work with local beads
		// Log warning but don't fail the spawn
		fmt.Printf("Warning: could not set up shared beads: %v\n", err)
	}

	// Return polecat with derived state (no issue assigned yet = idle)
	// State is derived from beads, not stored in state.json
	now := time.Now()
	polecat := &Polecat{
		Name:      name,
		Rig:       m.rig.Name,
		State:     StateIdle, // No issue assigned yet
		ClonePath: polecatPath,
		Branch:    branchName,
		CreatedAt: now,
		UpdatedAt: now,
	}

	return polecat, nil
}

// Remove deletes a polecat worktree.
// If force is true, removes even with uncommitted changes (but not stashes/unpushed).
// Use nuclear=true to bypass ALL safety checks.
func (m *Manager) Remove(name string, force bool) error {
	return m.RemoveWithOptions(name, force, false)
}

// RemoveWithOptions deletes a polecat worktree with explicit control over safety checks.
// force=true: bypass uncommitted changes check (legacy behavior)
// nuclear=true: bypass ALL safety checks including stashes and unpushed commits
func (m *Manager) RemoveWithOptions(name string, force, nuclear bool) error {
	if !m.exists(name) {
		return ErrPolecatNotFound
	}

	polecatPath := m.polecatDir(name)
	polecatGit := git.NewGit(polecatPath)

	// Check for uncommitted work unless bypassed
	if !nuclear {
		status, err := polecatGit.CheckUncommittedWork()
		if err == nil && !status.Clean() {
			// For backward compatibility: force only bypasses uncommitted changes, not stashes/unpushed
			if force {
				// Force mode: allow uncommitted changes but still block on stashes/unpushed
				if status.StashCount > 0 || status.UnpushedCommits > 0 {
					return &UncommittedWorkError{PolecatName: name, Status: status}
				}
			} else {
				return &UncommittedWorkError{PolecatName: name, Status: status}
			}
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
	_ = mayorGit.WorktreePrune()

	// Release name back to pool if it's a pooled name
	m.namePool.Release(name)
	_ = m.namePool.Save()

	return nil
}

// AllocateName allocates a name from the name pool.
// Returns a pooled name (polecat-01 through polecat-50) if available,
// otherwise returns an overflow name (rigname-N).
func (m *Manager) AllocateName() (string, error) {
	// First reconcile pool with existing polecats to handle stale state
	m.ReconcilePool()

	name, err := m.namePool.Allocate()
	if err != nil {
		return "", err
	}

	if err := m.namePool.Save(); err != nil {
		return "", fmt.Errorf("saving pool state: %w", err)
	}

	return name, nil
}

// ReleaseName releases a name back to the pool.
// This is called when a polecat is removed.
func (m *Manager) ReleaseName(name string) {
	m.namePool.Release(name)
	_ = m.namePool.Save()
}

// Recreate removes an existing polecat and creates a fresh worktree.
// This ensures the polecat starts with the latest code from the base branch.
// The name is preserved (not released to pool) since we're recreating immediately.
// force controls whether to bypass uncommitted changes check.
func (m *Manager) Recreate(name string, force bool) (*Polecat, error) {
	if !m.exists(name) {
		return nil, ErrPolecatNotFound
	}

	polecatPath := m.polecatDir(name)
	branchName := fmt.Sprintf("polecat/%s", name)
	mayorPath := filepath.Join(m.rig.Path, "mayor", "rig")
	mayorGit := git.NewGit(mayorPath)
	polecatGit := git.NewGit(polecatPath)

	// Check for uncommitted work unless forced
	if !force {
		status, err := polecatGit.CheckUncommittedWork()
		if err == nil && !status.Clean() {
			return nil, &UncommittedWorkError{PolecatName: name, Status: status}
		}
	}

	// Remove the worktree (use force for git worktree removal)
	if err := mayorGit.WorktreeRemove(polecatPath, true); err != nil {
		// Fall back to direct removal
		if removeErr := os.RemoveAll(polecatPath); removeErr != nil {
			return nil, fmt.Errorf("removing polecat dir: %w", removeErr)
		}
	}

	// Prune stale worktree entries
	_ = mayorGit.WorktreePrune()

	// Delete the old branch so worktree starts fresh from current HEAD
	// Ignore error - branch may not exist (first recreate) or may fail to delete
	_ = mayorGit.DeleteBranch(branchName, true)

	// Check if branch still exists (deletion may have failed or branch was protected)
	branchExists, err := mayorGit.BranchExists(branchName)
	if err != nil {
		return nil, fmt.Errorf("checking branch existence: %w", err)
	}

	// Create worktree - handle both cases like Add() does
	if branchExists {
		// Branch still exists, create worktree using existing branch
		// This happens if delete failed (e.g., protected branch)
		if err := mayorGit.WorktreeAddExisting(polecatPath, branchName); err != nil {
			return nil, fmt.Errorf("creating worktree with existing branch: %w", err)
		}
	} else {
		// Branch was deleted, create fresh worktree with new branch from HEAD
		if err := mayorGit.WorktreeAdd(polecatPath, branchName); err != nil {
			return nil, fmt.Errorf("creating fresh worktree: %w", err)
		}
	}

	// Set up shared beads
	if err := m.setupSharedBeads(polecatPath); err != nil {
		fmt.Printf("Warning: could not set up shared beads: %v\n", err)
	}

	// Return fresh polecat
	now := time.Now()
	return &Polecat{
		Name:      name,
		Rig:       m.rig.Name,
		State:     StateIdle,
		ClonePath: polecatPath,
		Branch:    branchName,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// ReconcilePool syncs pool state with existing polecat directories.
// This should be called to recover from crashes or stale state.
func (m *Manager) ReconcilePool() {
	polecats, err := m.List()
	if err != nil {
		return
	}

	var names []string
	for _, p := range polecats {
		names = append(names, p.Name)
	}

	m.namePool.Reconcile(names)
	_ = m.namePool.Save()
}

// PoolStatus returns information about the name pool.
func (m *Manager) PoolStatus() (active int, names []string) {
	return m.namePool.ActiveCount(), m.namePool.ActiveNames()
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
// State is derived from beads assignee field:
// - If an issue is assigned to this polecat and is open/in_progress: StateWorking
// - If an issue is assigned but closed: StateDone
// - If no issue assigned: StateIdle
func (m *Manager) Get(name string) (*Polecat, error) {
	if !m.exists(name) {
		return nil, ErrPolecatNotFound
	}

	return m.loadFromBeads(name)
}

// SetState updates a polecat's state.
// In the beads model, state is derived from issue status:
// - StateWorking/StateActive: issue status set to in_progress
// - StateDone/StateIdle: assignee cleared from issue
// - StateStuck: issue status set to blocked (if supported)
// If beads is not available, this is a no-op.
func (m *Manager) SetState(name string, state State) error {
	if !m.exists(name) {
		return ErrPolecatNotFound
	}

	// Find the issue assigned to this polecat
	assignee := m.assigneeID(name)
	issue, err := m.beads.GetAssignedIssue(assignee)
	if err != nil {
		// If beads is not available, treat as no-op (state can't be changed)
		return nil
	}

	switch state {
	case StateWorking, StateActive:
		// Set issue to in_progress if there is one
		if issue != nil {
			status := "in_progress"
			if err := m.beads.Update(issue.ID, beads.UpdateOptions{Status: &status}); err != nil {
				return fmt.Errorf("setting issue status: %w", err)
			}
		}
	case StateDone, StateIdle:
		// Clear assignment when done/idle
		if issue != nil {
			empty := ""
			if err := m.beads.Update(issue.ID, beads.UpdateOptions{Assignee: &empty}); err != nil {
				return fmt.Errorf("clearing assignee: %w", err)
			}
		}
	case StateStuck:
		// Mark issue as blocked if supported, otherwise just note in issue
		if issue != nil {
			// For now, just keep the assignment - the issue's blocked_by would indicate stuck
			// We could add a status="blocked" here if beads supports it
		}
	}

	return nil
}

// AssignIssue assigns an issue to a polecat by setting the issue's assignee in beads.
func (m *Manager) AssignIssue(name, issue string) error {
	if !m.exists(name) {
		return ErrPolecatNotFound
	}

	// Set the issue's assignee to this polecat
	assignee := m.assigneeID(name)
	status := "in_progress"
	if err := m.beads.Update(issue, beads.UpdateOptions{
		Assignee: &assignee,
		Status:   &status,
	}); err != nil {
		return fmt.Errorf("setting issue assignee: %w", err)
	}

	return nil
}

// ClearIssue removes the issue assignment from a polecat.
// In the transient model, this transitions to Done state for cleanup.
// This clears the assignee from the currently assigned issue in beads.
// If beads is not available, this is a no-op.
func (m *Manager) ClearIssue(name string) error {
	if !m.exists(name) {
		return ErrPolecatNotFound
	}

	// Find the issue assigned to this polecat
	assignee := m.assigneeID(name)
	issue, err := m.beads.GetAssignedIssue(assignee)
	if err != nil {
		// If beads is not available, treat as no-op
		return nil
	}

	if issue == nil {
		// No issue assigned, nothing to clear
		return nil
	}

	// Clear the assignee from the issue
	empty := ""
	if err := m.beads.Update(issue.ID, beads.UpdateOptions{
		Assignee: &empty,
	}); err != nil {
		return fmt.Errorf("clearing issue assignee: %w", err)
	}

	return nil
}

// Wake transitions a polecat from idle to active.
// Deprecated: In the transient model, polecats start in working state.
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
// Deprecated: In the transient model, polecats are deleted when done.
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
// This clears the assignee from any assigned issue.
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

	// Clear the issue assignment
	return m.ClearIssue(name)
}

// Reset forces a polecat to idle state regardless of current state.
// This clears the assignee from any assigned issue.
func (m *Manager) Reset(name string) error {
	if !m.exists(name) {
		return ErrPolecatNotFound
	}

	// Clear the issue assignment
	return m.ClearIssue(name)
}

// loadFromBeads derives polecat state from beads assignee field.
// State is derived as follows:
// - If an issue is assigned to this polecat and is open/in_progress: StateWorking
// - If no issue assigned: StateIdle
func (m *Manager) loadFromBeads(name string) (*Polecat, error) {
	polecatPath := m.polecatDir(name)
	branchName := fmt.Sprintf("polecat/%s", name)

	// Query beads for assigned issue
	assignee := m.assigneeID(name)
	issue, err := m.beads.GetAssignedIssue(assignee)
	if err != nil {
		// If beads query fails, return basic polecat info
		// This allows the system to work even if beads is not available
		return &Polecat{
			Name:      name,
			Rig:       m.rig.Name,
			State:     StateIdle,
			ClonePath: polecatPath,
			Branch:    branchName,
		}, nil
	}

	// Derive state from issue
	state := StateIdle
	issueID := ""
	if issue != nil {
		issueID = issue.ID
		switch issue.Status {
		case "open", "in_progress":
			state = StateWorking
		case "closed":
			state = StateDone
		default:
			// Unknown status, assume working if assigned
			state = StateWorking
		}
	}

	return &Polecat{
		Name:      name,
		Rig:       m.rig.Name,
		State:     state,
		ClonePath: polecatPath,
		Branch:    branchName,
		Issue:     issueID,
	}, nil
}

// setupSharedBeads creates a redirect file so the polecat uses the rig's shared .beads database.
// This eliminates the need for git sync between polecat clones - all polecats share one database.
//
// Structure:
//
//	rig/
//	  .beads/              <- Shared database (ensured to exist)
//	  polecats/
//	    <name>/
//	      .beads/
//	        redirect       <- Contains "../../.beads"
func (m *Manager) setupSharedBeads(polecatPath string) error {
	// Ensure rig root has .beads/ directory
	rigBeadsDir := filepath.Join(m.rig.Path, ".beads")
	if err := os.MkdirAll(rigBeadsDir, 0755); err != nil {
		return fmt.Errorf("creating rig .beads dir: %w", err)
	}

	// Create polecat's .beads directory
	polecatBeadsDir := filepath.Join(polecatPath, ".beads")
	if err := os.MkdirAll(polecatBeadsDir, 0755); err != nil {
		return fmt.Errorf("creating polecat .beads dir: %w", err)
	}

	// Create redirect file pointing to rig's .beads
	// Path is relative from polecats/<name>/.beads/ to rig/.beads/
	redirectPath := filepath.Join(polecatBeadsDir, "redirect")
	redirectContent := "../../.beads\n"

	if err := os.WriteFile(redirectPath, []byte(redirectContent), 0644); err != nil {
		return fmt.Errorf("creating redirect file: %w", err)
	}

	return nil
}
