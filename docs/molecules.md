# Molecules: Composable Workflow Templates

This document covers the molecule system in depth, including pluggable molecules
and the code-review molecule design.

For an overview, see [architecture.md](architecture.md#molecules-composable-workflow-templates).

## Core Concepts

A **molecule** is a crystallized workflow pattern stored as a beads issue. When
instantiated on a parent issue, it creates child beads forming a DAG of steps.

| Concept | Description |
|---------|-------------|
| Molecule | Read-only workflow template (type=molecule in beads) |
| Step | Individual unit of work within a molecule |
| Bond | Dependency between steps (Needs: directive) |
| Instance | Concrete beads created when molecule is instantiated |

## Two Molecule Types

### Static Molecules

Steps are embedded in the molecule's description field:

```markdown
## Step: design
Think carefully about architecture...

## Step: implement
Write the code...
Needs: design

## Step: test
Run tests...
Needs: implement
```

**Use case**: Well-defined, fixed workflows (engineer-in-box, polecat-work).

**Commands**:
```bash
bd create --type=molecule --title="My Workflow" --description="..."
gt molecule instantiate mol-xyz --parent=issue-123
```

### Pluggable Molecules

Steps are discovered from directories. Each directory is a plugin:

```
~/gt/molecules/code-review/
├── discovery/
│   ├── file-census/CLAUDE.md
│   └── dep-graph/CLAUDE.md
├── structural/
│   └── architecture-review/CLAUDE.md
└── tactical/
    ├── security-scan/CLAUDE.md
    └── performance-review/CLAUDE.md
```

**Use case**: Extensible workflows where dimensions can be added/removed.

**Commands**:
```bash
gt molecule instantiate code-review --parent=issue-123 --scope=src/
```

## Molecule CLI Reference

The `bd mol` commands manage the molecule lifecycle: bonding (instantiation), squashing
(completion with digest), and burning (abandonment).

### bd mol bond

Instantiate a proto molecule into a runnable molecule (Mol or Wisp).

```bash
bd mol bond <proto-id> [--wisp] [--assignee=<addr>]
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `<proto-id>` | Yes | ID of the proto molecule to instantiate |
| `--wisp` | No | Create a Wisp (ephemeral) instead of Mol (durable) |
| `--assignee` | No | Address of the agent who will execute this molecule |

**Behavior:**

- **Default (Mol)**: Creates a durable molecule tracked in the main `.beads/` database.
  Steps become permanent issues that survive indefinitely.
- **With --wisp**: Creates a wisp (transient molecule) in `.beads-wisp/`. Steps are
  transient and will be cleaned up on squash or burn.

**Examples:**

```bash
# Create a durable Mol from engineer-in-box proto
bd mol bond mol-engineer-in-box

# Create an ephemeral Wisp assigned to a polecat
bd mol bond mol-quick-fix --wisp --assignee=gastown/polecats/toast

# Bond a proto with a specific template variant
bd mol bond mol-engineer-in-box --template=minimal
```

**Output:**

Returns the ID of the newly created molecule instance (e.g., `gt-xyz.exec-001`).

---

### bd mol squash

Complete a molecule and generate a digest (permanent summary record).

```bash
bd mol squash <mol-id> --summary='...'
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `<mol-id>` | Yes | ID of the molecule instance to squash |
| `--summary` | Yes | Summary of what was accomplished (agent-generated) |

**Behavior:**

- **For Mol (durable)**: Creates a digest issue in the permanent beads database.
  The digest contains the summary and links back to the original proto.
- **For Wisp (transient)**: Evaporates the wisp (deletes from `.beads-wisp/`)
  and creates a digest in the permanent database. The execution trace is gone,
  but the outcome is preserved.

**The summary is critical**: Agents generate the summary describing what was
accomplished. This becomes the permanent record. Beads is a tool - the intelligence
for summarization comes from the agent.

**Examples:**

```bash
# Squash a completed molecule with summary
bd mol squash gt-xyz.exec-001 --summary='Implemented user auth with JWT tokens. Added login/logout endpoints and middleware for protected routes.'

# Squash with multi-line summary (use quotes)
bd mol squash gt-xyz.exec-001 --summary='Fixed authentication bug.

Changes:
- Corrected token expiry calculation
- Added refresh token rotation
- Updated tests'
```

**Output:**

Returns the ID of the created digest (e.g., `gt-xyz.digest-001`).

---

### bd mol burn

Abandon a molecule without completing it. No digest is created.

```bash
bd mol burn <mol-id> [--reason='...']
```

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `<mol-id>` | Yes | ID of the molecule instance to burn |
| `--reason` | No | Explanation of why the molecule was abandoned |

**Behavior:**

- Discards all molecule state (steps, progress, artifacts)
- No digest is created - the molecule leaves no permanent record
- For Wisps: Simply deletes from `.beads-wisp/`
- For Mols: Marks as abandoned/closed without digest

**When to burn vs squash:**

| Situation | Use |
|-----------|-----|
| Work completed successfully | `squash` - preserve the outcome |
| Routine work, no audit needed | `burn` - no record necessary |
| Work abandoned/cancelled | `burn` - nothing to record |
| Experiment that failed | `burn` - don't clutter history |
| Investigation with findings | `squash` - findings are valuable |

**Examples:**

```bash
# Burn a molecule that's no longer needed
bd mol burn gt-xyz.exec-001

# Burn with explanation
bd mol burn gt-xyz.exec-001 --reason='Superseded by different approach in gt-abc'

# Burn a failed experiment
bd mol burn gt-xyz.exec-001 --reason='Approach proved unworkable - see notes in PR #123'
```

---

### Lifecycle Summary

```
Proto Molecule (template in catalog)
         │
         ▼ bd mol bond
    ┌────────────────────┐
    │ Mol (durable) or   │
    │ Wisp (ephemeral)   │
    └────────┬───────────┘
             │
    ┌────────┴────────┐
    ▼                 ▼
bd mol burn      bd mol squash
(no record)      (creates digest)
```

**Key insight**: The molecule lifecycle maps to Gas Town's steam engine metaphor:
- **Proto** = Fuel (frozen template)
- **Mol/Wisp** = Steam (active execution)
- **Digest** = Distillate (condensed permanent record)
- **Burn** = Steam dissipates (no condensation)
- **Squash** = Steam condenses (captured as digest)

## Plugin CLAUDE.md Format

Each plugin directory contains a CLAUDE.md with optional frontmatter:

```markdown
---
phase: tactical
needs: [structural-complete]
tier: sonnet
---

# Security Scan

## Objective

Identify security vulnerabilities in the target code.

## Focus Areas

- OWASP Top 10 vulnerabilities
- Injection attacks (SQL, command, LDAP)
- Authentication/authorization bypasses
- Hardcoded secrets
- Insecure deserialization

## Output

For each finding, create a bead with:
- Clear title describing the vulnerability
- File path and line numbers
- Severity (P0-P4)
- Suggested remediation

Tag findings with `label: security`.
```

### Frontmatter Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Execution phase: discovery, structural, tactical, synthesis |
| `needs` | list | Step references that must complete first |
| `tier` | string | Model hint: haiku, sonnet, opus |

### Phase Semantics

| Phase | Blocks | Parallelism | Purpose |
|-------|--------|-------------|---------|
| discovery | nothing | full | Inventory, gather data |
| structural | discovery | sequential | Big picture analysis |
| tactical | structural | per-component | Detailed review |
| synthesis | tactical | single | Aggregate results |

## Code Review Molecule

The code-review molecule is the reference implementation for pluggable molecules.

### Directory Structure

```
~/gt/molecules/code-review/
├── README.md                    # Molecule overview
│
├── discovery/                   # Phase 1: Parallel scouts
│   ├── file-census/
│   │   └── CLAUDE.md           # Inventory: sizes, ages, churn
│   ├── dep-graph/
│   │   └── CLAUDE.md           # Dependencies, cycles, inversions
│   ├── coverage-map/
│   │   └── CLAUDE.md           # Test coverage, dead code
│   └── duplication-scan/
│       └── CLAUDE.md           # Near-duplicates, copy-paste
│
├── structural/                  # Phase 2: Sequential for coherence
│   ├── architecture-review/
│   │   └── CLAUDE.md           # Structure vs domain alignment
│   ├── abstraction-analysis/
│   │   └── CLAUDE.md           # Wrong-layer wrangling
│   └── consolidation-planner/
│       └── CLAUDE.md           # What should be unified
│
├── tactical/                    # Phase 3: Parallel per hotspot
│   ├── security-scan/
│   │   └── CLAUDE.md           # OWASP, injection, auth
│   ├── performance-review/
│   │   └── CLAUDE.md           # N+1, caching, memory
│   ├── complexity-analysis/
│   │   └── CLAUDE.md           # Cyclomatic, nesting
│   ├── test-gaps/
│   │   └── CLAUDE.md           # Untested paths, edge cases
│   └── elegance-review/
│       └── CLAUDE.md           # Magic numbers, naming
│
└── synthesis/                   # Phase 4: Single coordinator
    └── aggregate/
        └── CLAUDE.md           # Dedupe, prioritize, sequence
```

### Discovery Phase Plugins

#### file-census

**Purpose**: Build inventory of what we're reviewing.

**Output**:
- Total files, lines, and size
- Files by age (old = potential legacy)
- Files by churn (high churn = hotspots)
- Largest files (candidates for splitting)

#### dep-graph

**Purpose**: Map dependencies and structure.

**Output**:
- Dependency graph (imports, requires)
- Circular dependencies
- Orphaned code (unreachable)
- Inverted dependencies (high-level depending on low-level)

#### coverage-map

**Purpose**: Understand test coverage.

**Output**:
- Overall coverage percentage
- Untested files/functions
- Coverage by component
- Dead code (never executed)

#### duplication-scan

**Purpose**: Find duplicated logic.

**Output**:
- Near-duplicate files
- Copy-paste code blocks
- Redundant implementations of same concept

### Structural Phase Plugins

#### architecture-review

**Purpose**: Assess high-level structure.

**Questions**:
- Does directory structure match domain concepts?
- Are boundaries clean between components?
- Is there a clear layering strategy?
- Are cross-cutting concerns (logging, auth) handled consistently?

**Output**: Structural findings as beads, with refactoring recommendations.

#### abstraction-analysis

**Purpose**: Find missing or wrong abstractions.

**Signs of problems**:
- Same boilerplate repeated
- Business logic mixed with infrastructure
- Leaky abstractions (implementation details exposed)
- Primitive obsession (should be domain types)

**Output**: Abstraction issues as beads.

#### consolidation-planner

**Purpose**: Identify what should be unified.

**Looks for**:
- Multiple implementations of same concept
- Similar code in different places
- Parallel hierarchies
- Scattered handling of same concern

**Output**: Consolidation recommendations as beads.

### Tactical Phase Plugins

These run in parallel, each agent reviewing assigned files/components.

#### security-scan

**Focus**:
- OWASP Top 10
- Injection vulnerabilities
- Authentication/authorization issues
- Secrets in code
- Insecure configurations

#### performance-review

**Focus**:
- N+1 queries
- Missing caching opportunities
- Memory leaks
- Unnecessary computation
- Blocking operations in hot paths

#### complexity-analysis

**Focus**:
- Cyclomatic complexity > 10
- Deep nesting (> 4 levels)
- Long functions (> 50 lines)
- God classes/files
- Complex conditionals

#### test-gaps

**Focus**:
- Untested public APIs
- Missing edge cases
- No error path testing
- Brittle tests (mock-heavy, order-dependent)

#### elegance-review

**Focus**:
- Magic numbers/strings
- Unclear naming
- Inconsistent style
- Missing documentation for complex logic
- Overly clever code

### Synthesis Phase

#### aggregate

**Purpose**: Combine all findings into actionable backlog.

**Tasks**:
1. Deduplicate similar findings
2. Group related issues
3. Establish fix dependencies (fix X before Y)
4. Prioritize by impact
5. Sequence for efficient fixing

**Output**: Prioritized backlog ready for swarming.

## Implementation Plan

### Phase 1: Pluggable Molecule Infrastructure

1. **Directory scanner** (`internal/molecule/scanner.go`)
   - Scan molecule directories for plugins
   - Parse CLAUDE.md frontmatter
   - Build plugin registry

2. **DAG builder** (`internal/molecule/dag.go`)
   - Assemble dependency graph from plugins
   - Respect phase ordering
   - Validate no cycles

3. **Instantiation** (`internal/molecule/instantiate.go`)
   - Create beads for each step
   - Wire dependencies
   - Support scope parameter

### Phase 2: Code Review Molecule

1. **Plugin directory structure**
   - Create ~/gt/molecules/code-review/
   - Write CLAUDE.md for each dimension

2. **Discovery plugins** (4)
   - file-census, dep-graph, coverage-map, duplication-scan

3. **Structural plugins** (3)
   - architecture-review, abstraction-analysis, consolidation-planner

4. **Tactical plugins** (5)
   - security-scan, performance-review, complexity-analysis, test-gaps, elegance-review

5. **Synthesis plugin** (1)
   - aggregate

### Phase 3: CLI Integration

1. **gt molecule scan** - Show discovered plugins
2. **gt molecule validate** - Validate plugin structure
3. **gt molecule instantiate** - Create beads from plugins
4. **gt review** - Convenience wrapper for code-review molecule

## Usage Examples

### Basic Code Review

```bash
# Run full code review on project
gt molecule instantiate code-review --parent=gt-review-001

# Check what's ready to work
bd ready

# Swarm it
gt swarm --parent=gt-review-001
```

### Scoped Review

```bash
# Review only src/auth/
gt molecule instantiate code-review --parent=gt-review-002 --scope=src/auth

# Review only tactical dimensions
gt molecule instantiate code-review --parent=gt-review-003 --phases=tactical
```

### Adding a Custom Dimension

```bash
# Create plugin directory
mkdir -p ~/gt/molecules/code-review/tactical/accessibility-review

# Add CLAUDE.md
cat > ~/gt/molecules/code-review/tactical/accessibility-review/CLAUDE.md << 'EOF'
---
phase: tactical
needs: [structural-complete]
tier: sonnet
---

# Accessibility Review

Check for WCAG 2.1 compliance issues...
EOF

# Now it's automatically included in code-review
gt molecule scan code-review
```

### Iteration

```bash
# First review pass
gt molecule instantiate code-review --parent=gt-review-001
# ... fix issues ...

# Second pass (fewer findings expected)
gt molecule instantiate code-review --parent=gt-review-002
# ... fix remaining issues ...

# Third pass (should be at noise floor)
gt molecule instantiate code-review --parent=gt-review-003
```

## Beads Generated by Reviews

Each review step generates findings as beads:

```
gt-sec-001  SQL injection in login()     type=bug  priority=1  label=security
gt-sec-002  Missing CSRF protection       type=bug  priority=2  label=security
gt-perf-001 N+1 query in user list       type=bug  priority=2  label=performance
gt-arch-001 Auth logic in controller     type=task priority=3  label=refactor
```

Findings link back to the review:
```
discovered-from: gt-review-001.tactical-security-scan
```

This enables querying: "What did the security scan find?"

## Feed the Beast Pattern

Code review is a **work generator**:

```
Low on beads?
     │
     ▼
gt molecule instantiate code-review
     │
     ▼
Generates 50-200 fix beads
     │
     ▼
Prioritize and swarm
     │
     ▼
Codebase improves overnight
     │
     ▼
Repeat weekly
```

## Cross-Project Dependencies and Parking

Molecules can hit external dependencies that block progress. See
[cross-project-deps.md](cross-project-deps.md) for the full design.

### Parking a Molecule

When a molecule step depends on a capability from another project:

```bash
# Polecat discovers external dependency not satisfied
gt park --step=gt-mol.3 --waiting="beads:mol-run-assignee"
```

The molecule enters a **parked** state (derived from: in_progress + no assignee + blocked step).

### Resuming a Parked Molecule

When the external dependency is satisfied:

```bash
# Manual resume
gt spawn --continue gt-mol-root

# Or automated via Deacon patrol (future)
```

### Parked State

"Parked" is not a new status - it's derived from existing fields:
- Molecule status: `in_progress`
- Molecule assignee: `null` (no polecat owns it)
- Has step with unsatisfied `blocked_by: external:...`

Query parked molecules:
```bash
bd list --status=in_progress --no-assignee --type=molecule
```

## Future Extensions

### Custom Molecule Types

Beyond code-review, pluggable molecules could support:

- **migration-analysis**: Database migrations, API versioning
- **onboarding-review**: New hire documentation gaps
- **compliance-audit**: Regulatory requirements check
- **dependency-audit**: Outdated/vulnerable dependencies

### Scheduled Reviews

```yaml
# In rig config
scheduled_molecules:
  - molecule: code-review
    scope: "**/*.go"
    schedule: "0 0 * * 0"  # Weekly Sunday midnight
    priority: 3
```

### Review Trends

Track findings over time:
```bash
gt review history --molecule=code-review
# Shows: findings per run, categories, fix rate
```
