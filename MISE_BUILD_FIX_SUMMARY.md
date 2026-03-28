# Mise Build Task Fix - Quick Reference

## Problem (1-Minute Summary)

`mise run build` **FAILS** with permission error:
```
operation not permitted: /Users/noor/Library/Caches/go-build/...
```

**Why:** Line 6 of `mise.toml` hardcodes `GOPATH = "/Users/noor/.local/gopath/1.26.1"` to home directory, which has permission restrictions.

---

## Solution (2 Files, 3 Environment Variables)

### File 1: Update `/Users/noor/Builds/context0/mise.toml`

**Replace lines 6-7:**

```toml
# OLD (BROKEN)
GOPATH = "/Users/noor/.local/gopath/1.26.1"
```

**With (NEW):**

```toml
# NEW (FIXED)
GOPATH = "./.gopath"
GOCACHE = "./.gocache"
GOMODCACHE = "./.gomodcache"
```

**Complete fixed file:**
```toml
[tools]
go = "1.26.1"

[env]
MISE_GO_SET_GOROOT = true
# Use project-local directories for Go caching
GOPATH = "./.gopath"
GOCACHE = "./.gocache"
GOMODCACHE = "./.gomodcache"

[tasks.build]
description = "Build context0 binary with FTS5 and sqlite-vec support"
run = "CGO_ENABLED=1 go build -tags fts5 -o context0 ."
```

### File 2: Create `/Users/noor/Builds/context0/.gitignore`

```gitignore
# Project-local Go caches (not to be committed)
.gopath/
.gocache/
.gomodcache/
.mise-cache/

# Local mise state
.mise/

# Build output (optional)
/context0

# IDE
.vscode/
.idea/
*.swp
*.swo
*~
```

---

## Apply Fix (Copy-Paste Commands)

```bash
cd /Users/noor/Builds/context0

# Backup original
cp mise.toml mise.toml.backup

# Update mise.toml
cat > mise.toml << 'EOF'
[tools]
go = "1.26.1"

[env]
MISE_GO_SET_GOROOT = true
# Use project-local directories for Go caching
GOPATH = "./.gopath"
GOCACHE = "./.gocache"
GOMODCACHE = "./.gomodcache"

[tasks.build]
description = "Build context0 binary with FTS5 and sqlite-vec support"
run = "CGO_ENABLED=1 go build -tags fts5 -o context0 ."
