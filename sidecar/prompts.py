"""Prompt templates for the three sidecar-powered commands.

All user-visible behaviour of ``context0 ask``, ``context0 exec``, and
``context0 codemap discover`` is driven by the prompts in this file.
Tune here; logic lives in ask.py, ralph.py, and server.py respectively.

Each command exposes:
  <CMD>_SYSTEM   — system message  (role: "system")
  <CMD>_*        — one or more user-message templates  (role: "user")
                   Templates use str.format() placeholders: {query}, {project}, …
"""

from __future__ import annotations

# ===========================================================================
# context0 ask
# ===========================================================================
#
# Two-phase: PLAN picks which CLI commands to run; COMPRESS turns their
# combined output into a single developer-facing answer.

# ---------------------------------------------------------------------------
# ask — plan phase
# ---------------------------------------------------------------------------

ASK_PLAN_SYSTEM = """\
You are the planner for context0, a project knowledge tool for software developers.
Your only job is to output a JSON array — nothing else.
No markdown, no explanation, no prose."""

ASK_PLAN_USER = """\
Decide which context0 commands to run to answer the developer query below.

AVAILABLE COMMANDS
Each command is a JSON array of string tokens:

  Memory (project knowledge store):
    ["memory", "query", "<keywords or question>"]
      → full-text + semantic search across saved project notes

  Agenda (task tracker):
    ["agenda", "plan", "list"]
      → list all active plans with their tasks
    ["agenda", "plan", "search", "<keywords>"]
      → search plan titles and descriptions

  Codemap (symbol graph — requires index):
    ["codemap", "find", "<SymbolName>"]
      → locate a symbol definition across the project
    ["codemap", "outline", "<relative/file/path>"]
      → list all symbols defined in one file
    ["codemap", "impact", "<SymbolName>"]
      → which symbols would break if this one changed
    ["codemap", "status"]
      → index health: node/edge counts, last updated

RULES
- Return a JSON array of at most 4 commands.
- Choose only the commands genuinely needed — omit anything speculative.
- If no command is needed (general question, greeting, etc.) return [].
- Use exact symbol names and file paths where known; do not guess.

QUERY
{query}"""

# ---------------------------------------------------------------------------
# ask — compress phase
# ---------------------------------------------------------------------------

ASK_COMPRESS_SYSTEM = """\
You are a senior software-engineering assistant.
Answer the developer's question accurately and concisely.
Base your answer solely on the provided context — do not invent facts."""

ASK_COMPRESS_USER = """\
Answer the query below using ONLY the context provided.

Guidelines:
- Be direct and specific; avoid filler phrases.
- Use 2–4 sentences for factual answers; a short bullet list when listing items.
- If the context is incomplete, say so briefly and answer what you can.
- Do not mention that you are summarising or that you used a tool.

QUERY
{query}

CONTEXT
{context}"""

# Fallback when no context was gathered (no commands planned or all failed).
ASK_DIRECT_USER = "{query}"


# ===========================================================================
# context0 exec  (Ralph-loop repair)
# ===========================================================================
#
# Used by ralph.py when a script fails: the model receives the original
# script + error and must return a corrected version.

EXEC_REPAIR_SYSTEM = """\
You are an expert Python programmer performing automated script repair.
Output ONLY the corrected Python source code.
Do not include any explanation, comments about your changes, or markdown fences."""

EXEC_REPAIR_USER = """\
The script below failed with the error shown. Fix it so it runs correctly.

Common causes to check first:
- Missing or incorrect imports
- Wrong variable / attribute names
- Off-by-one index errors
- Incorrect subprocess argument lists
- Missing error handling for None / empty results

ORIGINAL SCRIPT
```python
{script}
```

ERROR
{error}

Return the fixed script only."""


# ===========================================================================
# context0 codemap discover
# ===========================================================================
#
# Used by server.py to generate an fd / rg Python script for non-indexed
# languages or ad-hoc structural queries.

DISCOVER_SYSTEM = """\
You are a code-search assistant for software development projects.
Generate a self-contained Python script that answers a codebase query using
subprocess calls to fd (find files) and/or rg (ripgrep).
Output ONLY runnable Python source — no explanation, no markdown fences."""

DISCOVER_USER = """\
Write a Python script that answers the query about the codebase below.

PROJECT ROOT
{project}

QUERY
{query}

REQUIREMENTS
- Use subprocess to call fd and/or rg; do not use os.walk or glob.
- Print only the most relevant results to stdout (cap at 40 lines).
- If a search returns nothing, print a short "not found" message instead of
  being silent.
- The script must be runnable as-is with no arguments.
- Use the project root as the search root for all commands.
- Prefer rg for content searches, fd for file/directory structure searches.
- rg flag notes: use -g "*.ext" for glob/file-type filtering (NOT --include),
  use -l to list matching files only, use -n for line numbers.

Return the Python script only."""
