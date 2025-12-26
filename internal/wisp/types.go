// Package wisp provides hook file support for Gas Town agents.
//
// DEPRECATED: Hook files are deprecated in favor of pinned beads.
// Work is now tracked via beads with status=pinned and assignee=agent,
// which can be discovered via query rather than explicit file management.
//
// Commands like `gt hook`, `gt sling`, `gt handoff` now use:
//
//	bd update <bead> --status=pinned --assignee=<agent>
//
// On session start, agents query for pinned beads rather than reading hook files.
// This follows Gas Town's "discovery over explicit state" principle.
//
// The hook file functions are kept for backward compatibility but are deprecated.
// Old hook files:
//   - hook-<agent>.json files tracked what bead was assigned to an agent
//   - Created by `gt hook`, `gt sling`, `gt handoff`
//   - Read on session start to restore work context
//   - Burned after pickup
package wisp

import (
	"strings"
	"time"
)

// WispType identifies the kind of hook file.
type WispType string

const (
	// TypeSlungWork is a hook that attaches a bead to an agent's hook.
	// Created by `gt hook`, `gt sling`, or `gt handoff`, and burned after pickup.
	TypeSlungWork WispType = "slung-work"
)

// WispDir is the directory where hook files are stored.
// Hook files (hook-<agent>.json) live alongside other beads data.
const WispDir = ".beads"

// HookPrefix is the filename prefix for hook files.
const HookPrefix = "hook-"

// HookSuffix is the filename suffix for hook files.
const HookSuffix = ".json"

// Wisp is the common header for hook files.
type Wisp struct {
	// Type identifies what kind of hook file this is.
	Type WispType `json:"type"`

	// CreatedAt is when the hook was created.
	CreatedAt time.Time `json:"created_at"`

	// CreatedBy identifies who created the hook (e.g., "crew/joe", "deacon").
	CreatedBy string `json:"created_by"`
}

// SlungWork represents work attached to an agent's hook.
// Created by `gt hook`, `gt sling`, or `gt handoff` and burned after pickup.
type SlungWork struct {
	Wisp

	// BeadID is the issue/bead to work on (e.g., "gt-xxx").
	BeadID string `json:"bead_id"`

	// Formula is the optional formula/form to apply to the work.
	// When set, this creates scaffolding around the target bead.
	// Used by `gt sling <formula> --on <bead>`.
	Formula string `json:"formula,omitempty"`

	// Context is optional additional context from the slinger.
	Context string `json:"context,omitempty"`

	// Subject is optional subject line (used in handoff mail).
	Subject string `json:"subject,omitempty"`

	// Args is optional natural language instructions for the formula executor.
	// Example: "patch release" or "focus on security issues"
	// The LLM executor interprets these instructions - no schema needed.
	Args string `json:"args,omitempty"`
}

// NewSlungWork creates a new slung work hook file.
func NewSlungWork(beadID, createdBy string) *SlungWork {
	return &SlungWork{
		Wisp: Wisp{
			Type:      TypeSlungWork,
			CreatedAt: time.Now(),
			CreatedBy: createdBy,
		},
		BeadID: beadID,
	}
}

// HookFilename returns the filename for an agent's hook file.
// Agent identities may contain slashes (e.g., "gastown/crew/max"),
// which are replaced with underscores to create valid filenames.
func HookFilename(agent string) string {
	safe := strings.ReplaceAll(agent, "/", "_")
	return HookPrefix + safe + HookSuffix
}

// AgentFromHookFilename extracts the agent identity from a hook filename.
// Reverses the slash-to-underscore transformation done by HookFilename.
func AgentFromHookFilename(filename string) string {
	if len(filename) <= len(HookPrefix)+len(HookSuffix) {
		return ""
	}
	if filename[:len(HookPrefix)] != HookPrefix {
		return ""
	}
	if filename[len(filename)-len(HookSuffix):] != HookSuffix {
		return ""
	}
	safe := filename[len(HookPrefix) : len(filename)-len(HookSuffix)]
	return strings.ReplaceAll(safe, "_", "/")
}
