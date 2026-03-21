// Package pkgmgr — metadata.go
// Per-language-server install metadata.
package pkgmgr

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// installFn is a function that installs a binary and returns its path.
type installFn func(ctx context.Context, name string) (string, error)

// meta bundles install information for a single binary.
type meta struct {
	install installFn
}

// binaryMeta maps binary names to their install metadata.
var binaryMeta = map[string]meta{
	"gopls": {
		install: goInstall("golang.org/x/tools/gopls@latest"),
	},
	"pylsp": {
		install: pipInstall("python-lsp-server"),
	},
	"typescript-language-server": {
		install: npmInstall("typescript-language-server", "typescript"),
	},
	"lua-language-server": {
		install: manualInstall("lua-language-server", "https://github.com/LuaLS/lua-language-server/releases"),
	},
}

// goInstall returns an installFn that runs `go install <pkg>`.
func goInstall(pkg string) installFn {
	return func(ctx context.Context, name string) (string, error) {
		if err := runCmdCtx(ctx, "go", "install", pkg); err != nil {
			return "", fmt.Errorf("go install %s: %w", pkg, err)
		}
		path, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("go install %s succeeded but binary not found: %w", pkg, err)
		}
		return path, nil
	}
}

// pipInstall returns an installFn that runs `pip install <pkg>`.
func pipInstall(pkg string) installFn {
	return func(ctx context.Context, name string) (string, error) {
		pip := "pip3"
		if runtime.GOOS == "windows" {
			pip = "pip"
		}
		if err := runCmdCtx(ctx, pip, "install", pkg); err != nil {
			return "", fmt.Errorf("pip install %s: %w", pkg, err)
		}
		path, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("pip install %s succeeded but binary not found: %w", pkg, err)
		}
		return path, nil
	}
}

// npmInstall returns an installFn that runs `npm install -g <pkgs...>`.
func npmInstall(pkgs ...string) installFn {
	return func(ctx context.Context, name string) (string, error) {
		args := append([]string{"install", "-g"}, pkgs...)
		if err := runCmdCtx(ctx, "npm", args...); err != nil {
			return "", fmt.Errorf("npm install %s: %w", strings.Join(pkgs, " "), err)
		}
		path, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("npm install succeeded but binary not found: %w", err)
		}
		return path, nil
	}
}

// manualInstall attempts a direct download for single-binary releases.
// Falls back to an instructional error if the platform is not handled.
func manualInstall(name, releaseURL string) installFn {
	return func(ctx context.Context, _ string) (string, error) {
		url := releaseDownloadURL(name)
		if url == "" {
			return "", fmt.Errorf(
				"pkgmgr: cannot auto-install %s on %s/%s — please install manually: %s",
				name, runtime.GOOS, runtime.GOARCH, releaseURL,
			)
		}
		return downloadBinary(ctx, name, url)
	}
}

// releaseDownloadURL returns a direct download URL for known binary releases.
func releaseDownloadURL(name string) string {
	if name != "lua-language-server" {
		return ""
	}
	const version = "3.7.4"
	osMap := map[string]string{"darwin": "darwin", "linux": "linux", "windows": "win32"}
	archMap := map[string]string{"amd64": "x64", "arm64": "arm64"}
	osStr, ok1 := osMap[runtime.GOOS]
	archStr, ok2 := archMap[runtime.GOARCH]
	if !ok1 || !ok2 {
		return ""
	}
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf(
		"https://github.com/LuaLS/lua-language-server/releases/download/%s/lua-language-server-%s-%s-%s.%s",
		version, version, osStr, archStr, ext,
	)
}

// downloadBinary fetches url, writes it to the install dir, and makes it executable.
func downloadBinary(ctx context.Context, name, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("pkgmgr: download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pkgmgr: download %s: HTTP %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "ctx0-"+name+"-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()

	dest, err := binaryPath(name)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		// Cross-device: copy manually.
		if err2 := copyFile(tmpName, dest); err2 != nil {
			return "", err2
		}
	}
	return dest, nil
}

// copyFile copies src to dst with mode 0755.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// runCmdCtx runs an external command, discarding output.
func runCmdCtx(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
