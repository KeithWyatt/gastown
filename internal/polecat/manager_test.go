package polecat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/rig"
)

func TestStateIsActive(t *testing.T) {
	tests := []struct {
		state  State
		active bool
	}{
		{StateWorking, true},
		{StateDone, false},
		{StateStuck, false},
		// Legacy states are treated as active
		{StateIdle, true},
		{StateActive, true},
	}

	for _, tt := range tests {
		if got := tt.state.IsActive(); got != tt.active {
			t.Errorf("%s.IsActive() = %v, want %v", tt.state, got, tt.active)
		}
	}
}

func TestStateIsWorking(t *testing.T) {
	tests := []struct {
		state   State
		working bool
	}{
		{StateIdle, false},
		{StateActive, false},
		{StateWorking, true},
		{StateDone, false},
		{StateStuck, false},
	}

	for _, tt := range tests {
		if got := tt.state.IsWorking(); got != tt.working {
			t.Errorf("%s.IsWorking() = %v, want %v", tt.state, got, tt.working)
		}
	}
}

func TestPolecatSummary(t *testing.T) {
	p := &Polecat{
		Name:  "Toast",
		State: StateWorking,
		Issue: "gt-abc",
	}

	summary := p.Summary()
	if summary.Name != "Toast" {
		t.Errorf("Name = %q, want Toast", summary.Name)
	}
	if summary.State != StateWorking {
		t.Errorf("State = %v, want StateWorking", summary.State)
	}
	if summary.Issue != "gt-abc" {
		t.Errorf("Issue = %q, want gt-abc", summary.Issue)
	}
}

func TestListEmpty(t *testing.T) {
	root := t.TempDir()
	r := &rig.Rig{
		Name: "test-rig",
		Path: root,
	}
	m := NewManager(r, git.NewGit(root))

	polecats, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(polecats) != 0 {
		t.Errorf("polecats count = %d, want 0", len(polecats))
	}
}

func TestGetNotFound(t *testing.T) {
	root := t.TempDir()
	r := &rig.Rig{
		Name: "test-rig",
		Path: root,
	}
	m := NewManager(r, git.NewGit(root))

	_, err := m.Get("nonexistent")
	if err != ErrPolecatNotFound {
		t.Errorf("Get = %v, want ErrPolecatNotFound", err)
	}
}

func TestRemoveNotFound(t *testing.T) {
	root := t.TempDir()
	r := &rig.Rig{
		Name: "test-rig",
		Path: root,
	}
	m := NewManager(r, git.NewGit(root))

	err := m.Remove("nonexistent", false)
	if err != ErrPolecatNotFound {
		t.Errorf("Remove = %v, want ErrPolecatNotFound", err)
	}
}

func TestPolecatDir(t *testing.T) {
	r := &rig.Rig{
		Name: "test-rig",
		Path: "/home/user/ai/test-rig",
	}
	m := NewManager(r, git.NewGit(r.Path))

	dir := m.polecatDir("Toast")
	expected := "/home/user/ai/test-rig/polecats/Toast"
	if dir != expected {
		t.Errorf("polecatDir = %q, want %q", dir, expected)
	}
}

func TestStateFile(t *testing.T) {
	r := &rig.Rig{
		Name: "test-rig",
		Path: "/home/user/ai/test-rig",
	}
	m := NewManager(r, git.NewGit(r.Path))

	file := m.stateFile("Toast")
	expected := "/home/user/ai/test-rig/polecats/Toast/state.json"
	if file != expected {
		t.Errorf("stateFile = %q, want %q", file, expected)
	}
}

func TestStatePersistence(t *testing.T) {
	root := t.TempDir()
	polecatDir := filepath.Join(root, "polecats", "Test")
	if err := os.MkdirAll(polecatDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	r := &rig.Rig{
		Name: "test-rig",
		Path: root,
	}
	m := NewManager(r, git.NewGit(root))

	// Save state
	polecat := &Polecat{
		Name:      "Test",
		Rig:       "test-rig",
		State:     StateWorking,
		ClonePath: polecatDir,
		Issue:     "gt-xyz",
	}
	if err := m.saveState(polecat); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	// Load state
	loaded, err := m.loadState("Test")
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}

	if loaded.Name != "Test" {
		t.Errorf("Name = %q, want Test", loaded.Name)
	}
	if loaded.State != StateWorking {
		t.Errorf("State = %v, want StateWorking", loaded.State)
	}
	if loaded.Issue != "gt-xyz" {
		t.Errorf("Issue = %q, want gt-xyz", loaded.Issue)
	}
}

func TestListWithPolecats(t *testing.T) {
	root := t.TempDir()

	// Create some polecat directories with state files
	for _, name := range []string{"Toast", "Cheedo"} {
		polecatDir := filepath.Join(root, "polecats", name)
		if err := os.MkdirAll(polecatDir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	r := &rig.Rig{
		Name: "test-rig",
		Path: root,
	}
	m := NewManager(r, git.NewGit(root))

	polecats, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(polecats) != 2 {
		t.Errorf("polecats count = %d, want 2", len(polecats))
	}
}

func TestSetState(t *testing.T) {
	root := t.TempDir()
	polecatDir := filepath.Join(root, "polecats", "Test")
	if err := os.MkdirAll(polecatDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	r := &rig.Rig{
		Name: "test-rig",
		Path: root,
	}
	m := NewManager(r, git.NewGit(root))

	// Initial state
	if err := m.saveState(&Polecat{Name: "Test", State: StateIdle}); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	// Update state
	if err := m.SetState("Test", StateActive); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	// Verify
	polecat, err := m.Get("Test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if polecat.State != StateActive {
		t.Errorf("State = %v, want StateActive", polecat.State)
	}
}

func TestAssignIssue(t *testing.T) {
	root := t.TempDir()
	polecatDir := filepath.Join(root, "polecats", "Test")
	if err := os.MkdirAll(polecatDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	r := &rig.Rig{
		Name: "test-rig",
		Path: root,
	}
	m := NewManager(r, git.NewGit(root))

	// Initial state
	if err := m.saveState(&Polecat{Name: "Test", State: StateIdle}); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	// Assign issue
	if err := m.AssignIssue("Test", "gt-abc"); err != nil {
		t.Fatalf("AssignIssue: %v", err)
	}

	// Verify
	polecat, err := m.Get("Test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if polecat.Issue != "gt-abc" {
		t.Errorf("Issue = %q, want gt-abc", polecat.Issue)
	}
	if polecat.State != StateWorking {
		t.Errorf("State = %v, want StateWorking", polecat.State)
	}
}

func TestClearIssue(t *testing.T) {
	root := t.TempDir()
	polecatDir := filepath.Join(root, "polecats", "Test")
	if err := os.MkdirAll(polecatDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	r := &rig.Rig{
		Name: "test-rig",
		Path: root,
	}
	m := NewManager(r, git.NewGit(root))

	// Initial state with issue
	if err := m.saveState(&Polecat{Name: "Test", State: StateWorking, Issue: "gt-abc"}); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	// Clear issue
	if err := m.ClearIssue("Test"); err != nil {
		t.Fatalf("ClearIssue: %v", err)
	}

	// Verify - in ephemeral model, ClearIssue transitions to Done
	polecat, err := m.Get("Test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if polecat.Issue != "" {
		t.Errorf("Issue = %q, want empty", polecat.Issue)
	}
	if polecat.State != StateDone {
		t.Errorf("State = %v, want StateDone", polecat.State)
	}
}
