---
name: project-agendas
description: Create and manage structured task plans with acceptance criteria using context0's Agenda Engine. Use when starting multi-step tasks, tracking progress, marking tasks complete, or searching for existing plans. Triggers on agenda, plan, todo, tasks, progress, checklist.
license: GPL-3.0
compatibility: Requires context0 binary in PATH.
allowed-tools: Bash
---

# Project Agendas

Use this skill to create and manage structured task plans using context0's Agenda Engine.

## When to use

- You are starting a multi-step task and want to track progress
- You need to look up an existing plan for the current project
- You want to mark tasks complete, reopen them, or update a plan
- You need to search for a previously created agenda by keyword

## CLI commands

```
context0 agenda create  --title <t> --description <d> [--task <t>]... [--task-guard <g>]...
context0 agenda list    [--all]
context0 agenda get     <id>
context0 agenda search  <query> [--limit <n>]
context0 agenda task    done   <agenda-id> <task-number>
context0 agenda task    reopen <agenda-id> <task-number>
context0 agenda update  <id> [--title <t>] [--description <d>] [--deactivate] [--tasks <json>]
context0 agenda delete  <id>
```

Add `--project <dir>` to any command to target a specific project directory instead of CWD.

## Workflow

### Creating an agenda

Create an agenda at the start of a multi-step task. Pass each task with a separate `--task` flag. Use `--task-guard` to set acceptance criteria for each corresponding task (positional pairing, same order as `--task`).

Example:
```
context0 agenda create \
  --title "Add authentication middleware" \
  --description "Implement JWT validation middleware and integrate it with all protected routes." \
  --task "Create JWT validation function in internal/auth/jwt.go" \
  --task-guard "jwt.go exists and compiles with go vet" \
  --task "Write unit tests for the validation function" \
  --task-guard "go test ./internal/auth/... passes" \
  --task "Integrate middleware into the router" \
  --task-guard "Protected routes return 401 without valid token" \
  --task "Update API documentation" \
  --task-guard "README documents the auth header format"
```

### Checking progress

List active agendas at the start of a session:

```
context0 agenda list
```

Use `--all` to include completed (inactive) agendas. Get full task details for a specific agenda:

```
context0 agenda get 7
```

### Marking tasks complete

**Before marking a task done, check its acceptance guard** (the "Done when:" line shown by `agenda get`). Only mark the task complete when the guard condition is satisfied. If a task has no guard, use your judgement that the work is complete.

Tasks are identified by **agenda ID** and **task number** (1-based, as shown by `agenda get`):

```
context0 agenda task done 7 1
```

Reopen a task if the guard condition is no longer met or work needs to resume:

```
context0 agenda task reopen 7 1
```

The engine automatically deactivates the agenda when all **required** (non-optional) tasks are complete.

### Searching agendas

Use keyword search to find a past or current agenda:

```
context0 agenda search "authentication middleware" --limit 5
```

### Updating an agenda

Update the description or append new tasks discovered mid-flight:

```
context0 agenda update 7 \
  --description "Also covers refresh token rotation." \
  --tasks '[{"task_order":5,"details":"Implement refresh token endpoint","is_optional":false}]'
```

### Deleting an agenda

Only inactive (completed) agendas can be deleted. Active agendas are protected:

```
context0 agenda delete 7
```

## Storage details

- Database: `~/.context0/<project>/agenda.sqlite`
- Search: FTS5 keyword search only (no embeddings required)
- Auto-deactivation: agenda is marked inactive when all non-optional tasks are completed

## Tips

- Call `agenda list` at the start of every session — if an active agenda exists, resume it rather than creating a duplicate
- Mark truly optional steps (docs updates, nice-to-haves) as optional via `--tasks` JSON so they don't block auto-deactivation
- Use `--task-guard` on `agenda create` to record the specific condition that must be true before a task counts as done
