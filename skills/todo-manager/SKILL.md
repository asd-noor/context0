# todo-manager

Use this skill to create and manage structured task plans using context0's Agenda Engine.

## When to use

- You are starting a multi-step task and want to track progress
- You need to look up an existing plan for the current project
- You want to mark tasks complete, reopen them, or update a plan
- You need to search for a previously created agenda by keyword

## CLI commands

```
context0 agenda create  --title <t> --description <d> [--task <t>]...
context0 agenda list    [--all]
context0 agenda get     <id>
context0 agenda search  <query> [--limit <n>]
context0 agenda task    done   <task-id>
context0 agenda task    reopen <task-id>
context0 agenda update  <id> [--title <t>] [--description <d>] [--deactivate] [--tasks <json>]
context0 agenda delete  <id>
```

Add `--project <dir>` to any command to target a specific project directory instead of CWD.

## Workflow

### Creating an agenda

Create an agenda at the start of a multi-step task. Pass each task with a separate `--task` flag. Mark non-critical tasks as optional in the JSON tasks update.

Example:
```
context0 agenda create \
  --title "Add authentication middleware" \
  --description "Implement JWT validation middleware and integrate it with all protected routes." \
  --task "Create JWT validation function in internal/auth/jwt.go" \
  --task "Write unit tests for the validation function" \
  --task "Integrate middleware into the router" \
  --task "Update API documentation"
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

After completing each task, mark it done immediately:

```
context0 agenda task done 12
```

Reopen a task if work needs to resume:

```
context0 agenda task reopen 12
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
- Use `acceptance_guard` in the tasks JSON to record the specific condition that must be true before a task counts as done
