---
name: openspec-update-change
description: Revise an OpenSpec change's planning artifacts while keeping them coherent. Use when requirements or design decisions change before implementation.
---

# Update an OpenSpec change

Update an existing OpenSpec change's planning artifacts. This workflow edits planning artifacts only; it never edits application code.

## Input

Optionally specify a change name and the requested revision. If the change or revision is unclear, ask the user before editing.

## Steps

### 1. Select the change

   If a change name is not provided, run:

   ```bash
   openspec list --json
   ```

   Ask the user to choose an active change when selection is ambiguous.

### 2. Resolve and read the change

   Run:

   ```bash
   openspec status --change "<name>" --json
   ```

   Use the returned `changeRoot`, `artifactPaths`, and `actionContext`; do not assume artifact paths. If the action context is `workspace-planning` and has no allowed edit roots, stop before editing.

   Read every existing proposal, spec, design, and task artifact listed by the status output.

### 3. Plan the revision

   Identify which artifacts are affected and how the revision should propagate between them. Preserve content unrelated to the requested change.

   Before each file edit, show the proposed change and ask the user to confirm it. Do not change implementation files or mark tasks complete unless explicitly requested.

### 4. Apply and check the revision

   Apply only the confirmed planning-artifact edits. Re-read the changed artifacts and check for contradictions between proposal, specs, design, and tasks.

   Run:

   ```bash
   openspec status --change "<name>"
   ```

   If appropriate, recommend `/opsx:continue` for missing artifacts, `/opsx:apply` for implementation, or `/opsx:archive` when the plan is complete.

## Guardrails

- Planning artifacts only; never edit application code.
- Read all existing artifacts before proposing changes.
- Confirm each artifact edit with the user.
- Preserve scenarios, decisions, and tasks not affected by the revision.
- Ask for clarification instead of guessing when the requested revision conflicts with the existing plan.
