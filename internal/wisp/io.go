package wisp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Common errors.
var (
	ErrNoWispDir   = errors.New("beads directory does not exist")
	ErrNoHook      = errors.New("no hook file found")
	ErrInvalidWisp = errors.New("invalid hook file format")
)

// EnsureDir ensures the .beads directory exists in the given root.
func EnsureDir(root string) (string, error) {
	dir := filepath.Join(root, WispDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create beads dir: %w", err)
	}
	return dir, nil
}

// WispPath returns the full path to a file in the beads directory.
func WispPath(root, filename string) string {
	return filepath.Join(root, WispDir, filename)
}

// HookPath returns the full path to an agent's hook file.
func HookPath(root, agent string) string {
	return WispPath(root, HookFilename(agent))
}

// WriteSlungWork writes a slung work hook to the agent's hook file.
//
// Deprecated: Hook files are deprecated. Use bd update --status=pinned instead.
// Work is now tracked via pinned beads (discoverable via query) rather than
// explicit hook files. This function is kept for backward compatibility.
func WriteSlungWork(root, agent string, sw *SlungWork) error {
	dir, err := EnsureDir(root)
	if err != nil {
		return err
	}

	path := filepath.Join(dir, HookFilename(agent))
	return writeJSON(path, sw)
}

// ReadHook reads the slung work from an agent's hook file.
// Returns ErrNoHook if no hook file exists.
//
// Deprecated: Hook files are deprecated. Query pinned beads instead.
// Use beads.List with Status=pinned and Assignee=agent.
func ReadHook(root, agent string) (*SlungWork, error) {
	path := HookPath(root, agent)

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, ErrNoHook
	}
	if err != nil {
		return nil, fmt.Errorf("read hook: %w", err)
	}

	var sw SlungWork
	if err := json.Unmarshal(data, &sw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidWisp, err)
	}

	if sw.Type != TypeSlungWork {
		return nil, fmt.Errorf("%w: expected slung-work, got %s", ErrInvalidWisp, sw.Type)
	}

	return &sw, nil
}

// BurnHook removes an agent's hook file after it has been picked up.
//
// Deprecated: Hook files are deprecated. Work is tracked via pinned beads
// which don't need burning - just unpin with bd update --status=open.
func BurnHook(root, agent string) error {
	path := HookPath(root, agent)
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil // already burned
	}
	return err
}

// HasHook checks if an agent has a hook file.
//
// Deprecated: Hook files are deprecated. Query pinned beads instead.
func HasHook(root, agent string) bool {
	path := HookPath(root, agent)
	_, err := os.Stat(path)
	return err == nil
}

// ListHooks returns a list of agents with active hooks.
//
// Deprecated: Hook files are deprecated. Query pinned beads instead.
func ListHooks(root string) ([]string, error) {
	dir := filepath.Join(root, WispDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var agents []string
	for _, e := range entries {
		if agent := AgentFromHookFilename(e.Name()); agent != "" {
			agents = append(agents, agent)
		}
	}
	return agents, nil
}

// writeJSON is a helper to write JSON files atomically.
func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}

	// Write to temp file then rename for atomicity
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) // cleanup on failure
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}
