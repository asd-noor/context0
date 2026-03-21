// Package util provides shared utilities for context0.
package util

import (
	"crypto/sha256"
	"fmt"
)

// NodeID returns a stable, deterministic node ID from (filePath, name, kind).
func NodeID(filePath, name, kind string) string {
	h := sha256.Sum256([]byte(filePath + ":" + name + ":" + kind))
	return fmt.Sprintf("%x", h[:16]) // 32-char hex, collision-safe for a codebase
}
