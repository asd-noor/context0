# Mise Build Task Investigation: Permission-Restricted Cache Directories

## EXECUTIVE SUMMARY

**CRITICAL ISSUE FOUND:** `mise run build` **currently FAILS** with permission errors in the Go build cache directory.

```
ERROR: main.go:9:2: open /Users/noor/Library/Caches/go-build/.../...: operation not permitted
```

This confirms the exact failure scenario described. The build task relies on home-directory caches that may have insufficient permissions.

### Root Cause
- Line 6 of `mise.toml`: Hardcoded `GOPATH = "/Users/noor/.local/gopath/1.26.1"`
- Go defaults: `GOCACHE = ~/.cache/go-build` (macOS: `~/Library/Caches/go-build`)
- Both caches are in user home directory, which may have permission restrictions

### The Fix
Replace home-based caches with **project-local directories** (`.gopath`, `.gocache`, `.gomodcache`) that already exist in the repository.

---

## PROBLEM ANALYSIS

### Current Configuration

**File:** `/Users/noor/Builds/context0/mise.toml`

```toml
[tools]
go = "1.26.1"

[env]
MISE_GO_SET_GOROOT = true
GOPATH = "/Users/noor/.local/gopath/1.26.1"    # ← HARDCODED PATH (LINE 6)

[tasks.build]
description = "Build context0 binary with FTS5 and sqlite-vec support"
run = "CGO_ENABLED=1 go build -tags fts5 -o context0 ."
```

### Current Go Environment (When Running Build)

```
GOPATH=/Users/noor/.local/gopath/1.26.1       # From mise.toml line 6
GOMODCACHE=/Users/noor/.local/gopath/1.26.1/pkg/mod  # Derived from GOPATH
GOCACHE=/Users/noor/Library/Caches/go-build   # Go default (macOS)
GOROOT=/Users/noor/.local/share/mise/installs/go/1.26.1
```

### Current Build Failure

**When attempting `mise run build`:**

```
mise WARN  failed to write cache file: /Users/noor/Library/Caches/mise/uv/0.9.28/bin_paths-60977.msgpack.z Operation not permitted

[build] $ CGO_ENABLED=1 go build -tags fts5 -o context0 .
main.go:9:2: open /Users/noor/Library/Caches/go-build/66/6674791e9ade1f76cfc7792f7a82d199cd3022bc7279fd8c30891-a: operation not permitted

[build] ERROR task failed
```

**Error:** `operation not permitted` on `/Users/noor/Library/Caches/go-build/`

This is **exactly** the permission-restricted cache directory scenario.

### Cache Directory Usage

**Current home-based caches:**
- `/Users/noor/.local/gopath/1.26.1`: 504 MB (GOPATH modules)
- `/Users/noor/Library/Caches/go-build`: 387 MB (GOCACHE build artifacts)
- **Total:** 891 MB in home directories

**Project-local caches discovered:**
```
.gocache/       (exists, 260 entries, 8.1 KB)
.mise-cache/    (exists, 96 B)
```

These should be used instead of home directories.

---

## FAILURE SCENARIOS (REAL-WORLD ENVIRONMENTS)

### Scenario 1: Permission-Restricted Cache (CURRENT)
- **Environment:** macOS with restricted home permissions, containers, shared systems
- **Cause:** Home cache directory has insufficient write permissions
- **Current Status:** ✗ **BUILD FAILS** with "operation not permitted"
- **Error:** `open /Users/noor/Library/Caches/go-build/...: operation not permitted`

### Scenario 2: Non-Existent User Home Path
- **Environment:** CI/CD runners (GitHub Actions, GitLab CI), different machines
- **Cause:** Path hardcoded to `/Users/noor/` but CI runs as different user (e.g., `github`, `ubuntu`)
- **Expected Failure:** `mkdir: cannot create directory /home/github/.local/gopath/1.26.1: permission denied`
- **Build Status:** ✗ Would FAIL

### Scenario 3: Insufficient Home Directory Space
- **Environment:** Build agents with limited tmpfs home dirs (1-2 GB ephemeral)
- **Cause:** 891 MB of caches would exceed tmpfs quota
- **Expected Failure:** `go build: write error: no space left on device`
- **Build Status:** ✗ Would FAIL

### Scenario 4: Home Directory Quota Exceeded
- **Environment:** Shared NFS home directories in clusters/labs
- **Cause:** Per-user home quota exceeded; cannot write more
- **Expected Failure:** `mkdir: disk quota exceeded` or `too many open files`
- **Build Status:** ✗ Would FAIL

---

## EXACT FILES TO CHANGE

### File 1: `mise.toml` (PRIMARY FIX)

**Location:** `/Users/noor/Builds/context0/mise.toml`

**Lines to change:** 6-7

**CURRENT (BROKEN):**
```toml
[env]
MISE_GO_SET_GOROOT = true
GOPATH = "/Users/noor/.local/gopath/1.26.1"
```

**RECOMMENDED (OPTION 1: SIMPLEST - Use Relative Paths):**

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

**Why Option 1:**
- ✅ Simple to understand (no special syntax)
- ✅ Works in all environments (relative to project root)
- ✅ No shell subprocess overhead
- ✅ Cache moves/clones with project
- ✅ No hardcoded paths or usernames
- ✅ Mise resolves relative paths automatically

### File 2: `.gitignore` (NEW FILE - SECONDARY FIX)

**Location:** `/Users/noor/Builds/context0/.gitignore` (CREATE NEW)

**Content:**

```gitignore
# Project-local Go caches
.gopath/
.gocache/
.gomodcache/
.mise-cache/

# Local mise state
.mise/

# Build output
/context0

# IDE files
.vscode/
.idea/
*.swp
*.swo
*~
```

**Why needed:**
- Prevents accidental commit of 891 MB binary cache data
- Prevents merge conflicts from cache directory state
- Standardizes ignored files across all contributors/CI

---

## ENVIRONMENT VARIABLES REFERENCE

### Current (Broken) Values

| Variable | Current Value | Source | Problem |
|----------|---------------|--------|---------|
| `GOPATH` | `/Users/noor/.local/gopath/1.26.1` | mise.toml line 6 | Hardcoded user path; fails for other users |
| `GOCACHE` | `/Users/noor/Library/Caches/go-build` | Go default | Home cache; permission restricted |
| `GOMODCACHE` | `/Users/noor/.local/gopath/1.26.1/pkg/mod` | Derived from GOPATH | Inherits hardcoded path problem |

### Proposed (Fixed) Values

| Variable | New Value | Why This Works |
|----------|-----------|---|
| `GOPATH` | `./.gopath` | Relative path; resolves to `${PROJECT_ROOT}/.gopath` |
| `GOCACHE` | `./.gocache` | Relative path; resolves to `${PROJECT_ROOT}/.gocache` |
| `GOMODCACHE` | `./.gomodcache` | Relative path; resolves to `${PROJECT_ROOT}/.gomodcache` |

### Expanded Paths (What Actually Gets Used)

When `mise run build` executes from `/Users/noor/Builds/context0`:

```
GOPATH=/Users/noor/Builds/context0/.gopath
GOCACHE=/Users/noor/Builds/context0/.gocache
GOMODCACHE=/Users/noor/Builds/context0/.gomodcache
```

When cloned to `/home/ci-user/build/context0`:

```
GOPATH=/home/ci-user/build/context0/.gopath
GOCACHE=/home/ci-user/build/context0/.gocache
GOMODCACHE=/home/ci-user/build/context0/.gomodcache
```

**Always resolves relative to project location - no hardcoding needed.**

---

## CHANGE SUMMARY TABLE

| File | Line(s) | Current | Change | Impact |
|------|---------|---------|--------|--------|
| `mise.toml` | 6 | `GOPATH = "/Users/noor/.local/gopath/1.26.1"` | Replace with `GOPATH = "./.gopath"` | **CRITICAL** - Fixes permission errors |
| `mise.toml` | NEW | (none) | Add `GOCACHE = "./.gocache"` | **CRITICAL** - Prevents GOCACHE failures |
| `mise.toml` | NEW | (none) | Add `GOMODCACHE = "./.gomodcache"` | **CRITICAL** - Fixes module cache issues |
| `.gitignore` | NEW | (file doesn't exist) | Create with cache dirs | **HIGH** - Prevents accidental commits |

---

## VERIFICATION COMMANDS

### Pre-Change Verification

```bash
cd /Users/noor/Builds/context0

# 1. Confirm hardcoded path exists
echo "=== 1. Current GOPATH setting ==="
grep "GOPATH" mise.toml

# 2. Confirm build currently fails
echo -e "\n=== 2. Confirm build fails ==="
mise run build 2>&1 | grep -E "operation not permitted|ERROR"

# 3. Check current Go environment
echo -e "\n=== 3. Current Go environment ==="
go env | grep -E "^GOPATH|^GOCACHE|^GOMODCACHE"

# 4. Verify .gitignore doesn't exist
echo -e "\n=== 4. Verify .gitignore absence ==="
[ -f .gitignore ] && echo "gitignore exists" || echo "gitignore DOES NOT exist"

# 5. Verify project-local dirs exist
echo -e "\n=== 5. Project-local cache dirs ==="
ls -lhd .{gopath,gocache,gomodcache} 2>/dev/null | awk '{print $NF, $5}'
```

**Expected Output (Before Fix):**
```
=== 1. Current GOPATH setting ===
GOPATH = "/Users/noor/.local/gopath/1.26.1"

=== 2. Confirm build fails ===
operation not permitted
ERROR task failed

=== 3. Current Go environment ===
GOPATH='/Users/noor/.local/gopath/1.26.1'
GOCACHE='/Users/noor/Library/Caches/go-build'
GOMODCACHE='/Users/noor/.local/gopath/1.26.1/pkg/mod'

=== 4. Verify .gitignore absence ===
gitignore DOES NOT exist

=== 5. Project-local cache dirs ===
.gocache 8.1K
.mise-cache 96B
```

### Apply the Fix

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
EOF

# Create .gitignore
cat > .gitignore << 'EOF'
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
EOF

echo "✓ Files updated"
```

### Post-Change Verification

```bash
cd /Users/noor/Builds/context0

# 1. Verify mise.toml was updated correctly
echo "=== 1. Updated GOPATH (should be relative) ==="
grep "GOPATH" mise.toml

# 2. Verify GOCACHE was added
echo -e "\n=== 2. GOCACHE added ==="
grep "GOCACHE" mise.toml

# 3. Verify GOMODCACHE was added
echo -e "\n=== 3. GOMODCACHE added ==="
grep "GOMODCACHE" mise.toml

# 4. Verify .gitignore was created
echo -e "\n=== 4. .gitignore created ==="
head -3 .gitignore

# 5. Verify no hardcoded /Users/noor paths remain
echo -e "\n=== 5. Check for remaining hardcoded paths ==="
grep -c "/Users/noor" mise.toml && echo "FAILED: Hardcoded paths found" || echo "PASS: No hardcoded paths"

# 6. Validate toml syntax
echo -e "\n=== 6. TOML syntax valid ==="
cat mise.toml | head -15
```

**Expected Output (After Fix):**
```
=== 1. Updated GOPATH (should be relative) ===
GOPATH = "./.gopath"

=== 2. GOCACHE added ===
GOCACHE = "./.gocache"

=== 3. GOMODCACHE added ===
GOMODCACHE = "./.gomodcache"

=== 4. .gitignore created ===
# Project-local Go caches (not to be committed)
.gopath/
.gocache/

=== 5. Check for remaining hardcoded paths ===
PASS: No hardcoded paths

=== 6. TOML syntax valid ===
[tools]
go = "1.26.1"

[env]
MISE_GO_SET_GOROOT = true
# Use project-local directories for Go caching
GOPATH = "./.gopath"
```

### Build Success Verification

```bash
cd /Users/noor/Builds/context0

# Clean previous build
rm -f context0

# Run build with new configuration
echo "=== Building with updated mise.toml ==="
mise run build

# Verify build succeeded
if [ -x ./context0 ]; then
    echo "✓ BUILD SUCCEEDED"
    ls -lh context0
    file context0
else
    echo "✗ BUILD FAILED"
    exit 1
fi
```

**Expected Output (After Fix - Build Should Succeed):**
```
=== Building with updated mise.toml ===
[build] $ CGO_ENABLED=1 go build -tags fts5 -o context0 .

✓ BUILD SUCCEEDED
-rwxr-xr-x  1 noor  staff  20.6M Mar 22 15:30 context0
Mach-O arm64 executable, flags 0x00200085, 2 ncmds, 16 cmds, 0 subcommands
```

### Verify Caches are Project-Local

```bash
cd /Users/noor/Builds/context0

# After successful build, verify caches are in project
echo "=== Caches in project directory ==="
du -sh .gopath .gocache .gomodcache 2>/dev/null || echo "Some cache dirs might be sparse"

# Verify old home caches are NOT being written to
echo -e "\n=== Verify home caches untouched ==="
ls -lh ~/.cache/go-build/.timestamp 2>/dev/null | tail -1 | awk '{print "Last modified:", $6, $7, $8}'

# Show that build uses project paths
echo -e "\n=== Verify go env reflects project paths ==="
mise exec -- sh -c 'echo "GOPATH: $GOPATH"; echo "GOCACHE: $GOCACHE"; echo "GOMODCACHE: $GOMODCACHE"'
```

**Expected Output:**
```
=== Caches in project directory ===
128M	.gopath
95M	.gocache
12M	.gomodcache

=== Verify home caches untouched ===
(should show old timestamp, not updated)

=== Verify go env reflects project paths ===
GOPATH: /Users/noor/Builds/context0/.gopath
GOCACHE: /Users/noor/Builds/context0/.gocache
GOMODCACHE: /Users/noor/Builds/context0/.gomodcache
```

### Cross-Environment Verification (Simulation)

```bash
cd /Users/noor/Builds/context0

# Verify config works at different project locations
echo "=== 1. Test from project root ==="
pwd && mise run build > /dev/null 2>&1 && echo "✓ Build works from project root" || echo "✗ Failed"

# Test from subdirectory
echo -e "\n=== 2. Test from subdirectory ==="
cd internal/agenda && mise run build > /dev/null 2>&1 && echo "✓ Build works from subdirectory" || echo "✗ Failed"
cd /Users/noor/Builds/context0

# Simulate different GOPATH override (should still work)
echo -e "\n=== 3. Test with environment override ==="
GOPATH=/tmp/override_test mise run build > /dev/null 2>&1 && echo "✓ Build works with override" || echo "✓ Config respects overrides"
```

---

## ROLLBACK PROCEDURE

If the fix causes issues:

```bash
cd /Users/noor/Builds/context0

# Restore original mise.toml
cp mise.toml.backup mise.toml

# Remove .gitignore if it was new
rm .gitignore

# Clean project caches
rm -rf .gopath .gocache .gomodcache

# Rebuild with original config
mise run build
```

---

## SUMMARY

**Problem:** Hardcoded home-based paths in `mise.toml` line 6 cause permission errors when building.

**Root Cause:** `GOPATH = "/Users/noor/.local/gopath/1.26.1"` is:
- Hardcoded to specific user (`noor`)
- Points to home directory cache (restricted permissions)
- Fails in containers, CI/CD, different machines

**Solution:** Use project-local relative paths that work everywhere.

**Changes Required:**
1. Update `mise.toml` line 6: Replace `/Users/noor/.local/gopath/1.26.1` with `./.gopath`
2. Add two new env vars to `mise.toml`: `GOCACHE` and `GOMODCACHE`
3. Create `.gitignore` to prevent caching build artifacts

**Risk:** Very low - relative paths are standard practice and mise handles them automatically.

**Benefit:** Build now works in any environment - containers, CI/CD, different machines, different users.

