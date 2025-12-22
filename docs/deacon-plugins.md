# Deacon Plugins

Town-level plugins that run during the Deacon's patrol loop.

## Overview

The Deacon's patrol includes a `plugin-run` step. Rather than hardcoding what
runs, we use **directory-based discovery** at `~/gt/plugins/`. Each plugin
directory contains enough information for the Deacon to decide whether to run
it and what to do.

Plugins are not pre-validated by tooling. The Deacon reads each plugin's
metadata and instructions, decides if the gate is open, and executes
accordingly.

## Directory Structure

```
~/gt/plugins/
├── beads-cleanup/
│   ├── plugin.md         # Gate info + instructions (combined)
│   └── state.json        # Auto-managed runtime state
├── health-report/
│   ├── plugin.md
│   └── state.json
└── wisp-pressure/
    ├── plugin.md
    └── state.json
```

Each plugin is a directory. If the directory exists, the plugin exists.

## Plugin Metadata (plugin.md)

The `plugin.md` file contains YAML frontmatter for gate configuration,
followed by markdown instructions for the Deacon.

```markdown
---
gate: cooldown
interval: 24h
parallel: true
---

# Beads Cleanup

Clean up old wisps daily.

## Actions

1. Run `bd cleanup --wisp --force`
2. Run `bd doctor` to verify health
3. Log summary of cleaned items
```

### Frontmatter Fields

| Field | Required | Description |
|-------|----------|-------------|
| `gate` | Yes | Gate type: `cooldown`, `cron`, `condition`, `event` |
| `interval` | For cooldown | Duration: `1h`, `24h`, `5m`, etc. |
| `schedule` | For cron | Cron expression: `0 9 * * *` |
| `check` | For condition | Command that outputs a number |
| `operator` | For condition | Comparison: `gt`, `lt`, `eq`, `ge`, `le` |
| `threshold` | For condition | Numeric threshold |
| `trigger` | For event | Event name: `startup`, `heartbeat` |
| `cooldown` | Optional | Min time between runs (for condition gates) |
| `parallel` | Optional | If true, can run concurrently with other plugins |

## Gate Types

### Cooldown

Run at most once per interval since last successful run.

```yaml
---
gate: cooldown
interval: 24h
---
```

### Cron

Run on a schedule (standard cron syntax).

```yaml
---
gate: cron
schedule: "0 9 * * *"   # 9am daily
---
```

### Condition

Run when a command's output crosses a threshold.

```yaml
---
gate: condition
check: "bd count --type=wisp"
operator: gt
threshold: 50
cooldown: 30m           # Don't spam even if condition stays true
---
```

The `check` command must output a single number. The Deacon compares it
against `threshold` using `operator`.

### Event

Run in response to specific events.

```yaml
---
gate: event
trigger: startup        # Run once when daemon starts
---
```

Triggers: `startup`, `heartbeat` (every patrol), `mail` (when inbox has items).

## State Management

The Deacon maintains `state.json` in each plugin directory:

```json
{
  "lastRun": "2025-12-21T10:00:00Z",
  "lastResult": "success",
  "runCount": 42,
  "nextEligible": "2025-12-22T10:00:00Z"
}
```

This is auto-managed. Plugins don't need to touch it.

## Parallel Execution

Plugins marked `parallel: true` can run concurrently. During `plugin-run`,
the Deacon:

1. Scans `~/gt/plugins/` for plugins with open gates
2. Groups them: parallel vs sequential
3. Launches parallel plugins using Task tool subagents
4. Runs sequential plugins one at a time
5. Waits for all to complete before continuing patrol

Sequential plugins (default) run in directory order. Use sequential for
plugins that modify shared state or have ordering dependencies.

## Example Plugins

### beads-cleanup (daily maintenance)

```markdown
---
gate: cooldown
interval: 24h
parallel: true
---

# Beads Cleanup

Daily cleanup of wisp storage.

## Actions

1. Run `bd cleanup --wisp --force` to remove old wisps
2. Run `bd doctor` to check for issues
3. Report count of cleaned items
```

### wisp-pressure (condition-triggered)

```markdown
---
gate: condition
check: "bd count --type=wisp"
operator: gt
threshold: 100
cooldown: 1h
parallel: true
---

# Wisp Pressure Relief

Triggered when wisp count gets too high.

## Actions

1. Run `bd cleanup --wisp --age=4h` (remove wisps > 4h old)
2. Check `bd count --type=wisp`
3. If still > 50, run `bd cleanup --wisp --age=1h`
4. Report final count
```

### health-report (scheduled)

```markdown
---
gate: cron
schedule: "0 9 * * *"
parallel: false
---

# Morning Health Report

Generate daily health summary for the overseer.

## Actions

1. Run `gt status` to get overall health
2. Run `bd stats` to get metrics
3. Check for stale agents (no heartbeat > 1h)
4. Send summary: `gt mail send --human -s "Morning Report" -m "..."`
```

### startup-check (event-triggered)

```markdown
---
gate: event
trigger: startup
parallel: false
---

# Startup Verification

Run once when the daemon starts.

## Actions

1. Verify `gt doctor` passes
2. Check all rigs have healthy Witnesses
3. Report any issues to Mayor inbox
```

## Patrol Integration

The `mol-deacon-patrol` step 3 (plugin-run) works as follows:

```markdown
## Step: plugin-run

Execute registered plugins whose gates are open.

1. Scan `~/gt/plugins/` for plugin directories
2. For each plugin, read `plugin.md` frontmatter
3. Check if gate is open (based on type and state.json)
4. Collect eligible plugins into parallel and sequential groups

5. For parallel plugins:
   - Use Task tool to spawn subagents for each
   - Each subagent reads the plugin's instructions and executes
   - Wait for all to complete

6. For sequential plugins:
   - Execute each in order
   - Read instructions, run actions, update state

7. Update state.json for each completed plugin
8. Log summary: "Ran N plugins (M parallel, K sequential)"
```

## Limitations

- **No polecat spawning**: Plugins cannot spawn polecats. If a plugin tries
  to use `gt spawn`, behavior is undefined. This may change in the future.

- **No cross-plugin dependencies**: Plugins don't declare dependencies on
  each other. If ordering matters, mark both as `parallel: false`.

- **No plugin-specific state API**: Plugins can write to their own directory
  if needed, but there's no structured API for plugin state beyond what the
  Deacon auto-manages.

## CLI Commands (Future)

```bash
gt plugins list              # Show all plugins, gates, status
gt plugins check             # Show plugins with open gates
gt plugins show <name>       # Detail view of one plugin
gt plugins run <name>        # Force-run (ignore gate)
```

These commands are not yet implemented. The Deacon reads the directory
structure directly during patrol.
