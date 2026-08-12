---
name: openspec-continue-change
description: Continue an OpenSpec change by creating its next artifact. Use when the user wants to progress a change one artifact at a time.
---

# Continue an OpenSpec change

Continue working on an OpenSpec change by creating the next artifact.

## Input

Optionally specify a change name. If omitted, select it from the active changes. If the request is ambiguous, ask the user to choose.

## Steps

### 1. Select the change

   Run:

   ```bash
   openspec list --json
   ```

   Present the active changes with their schema and progress. Do not guess when more than one change is available.

### 2. Read the current status

   Run:

   ```bash
   openspec status --change "<name>" --json
   ```

   Use the returned `changeRoot`, `artifactPaths`, `actionContext`, and artifact statuses. If the action context is `workspace-planning` and has no allowed edit roots, stop before editing.

   If `isComplete` is `true`, report that all artifacts are complete and suggest implementation or archive.

### 3. Create exactly one artifact

   Find the first artifact with status `ready`. If none is ready, show the status and explain the blocker.

   Get its instructions:

   ```bash
   openspec instructions <artifact-id> --change "<name>" --json
   ```

   Read every completed dependency listed by the instructions. Use the returned template, context, rules, and resolved output path to create the artifact. Do not copy the instruction context or rules into the artifact.

### 4. Verify progress

   Confirm that the resolved output file exists, then run:

   ```bash
   openspec status --change "<name>"
   ```

   Report the artifact created, current progress, and newly unlocked artifacts. Stop after one artifact.

## Guardrails

- Never skip or reorder artifacts.
- Always read completed dependencies before creating the next artifact.
- Create only one artifact per invocation.
- Ask before proceeding when requirements or the artifact path are unclear.
- Do not edit application code as part of this workflow.
