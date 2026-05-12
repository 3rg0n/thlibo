package install

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	builtins "github.com/3rg0n/thlibo/processors"
)

// MirrorBuiltins copies the embedded built-in processors tree onto
// disk at dest, preserving directory structure. Script processors
// need a real on-disk directory because the dispatcher chdirs+execs
// their entry files.
//
// Overwrites existing files (idempotent re-run updates in place).
// Does NOT touch files that aren't part of the embedded tree, so
// users can drop their own processors into the same directory.
func MirrorBuiltins(dest string) error {
	return mirrorFS(builtins.FS, ".", dest)
}

func mirrorFS(src fs.FS, srcRoot, destRoot string) error {
	return fs.WalkDir(src, srcRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := p
		if srcRoot != "." {
			rel = strings.TrimPrefix(p, srcRoot+"/")
		}
		target := filepath.Join(destRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		// Script entry files need to be executable; non-entry files
		// (processor.yaml, processor.md, README) don't. We keep the
		// permissions permissive enough for Python/bash to exec the
		// script without caring whether it was .py or .sh.
		mode := os.FileMode(0o600)
		if isExecutable(filepath.Base(target)) {
			mode = 0o700
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return fmt.Errorf("mirror %s: %w", target, err)
		}
		// On Unix, WriteFile honours the mode only if the file did
		// not already exist. Explicitly chmod so a re-mirror that
		// overwrites a file still ends up with the right bits.
		if err := os.Chmod(target, mode); err != nil {
			return fmt.Errorf("chmod %s: %w", target, err)
		}
		return nil
	})
}

func isExecutable(name string) bool {
	switch filepath.Ext(strings.ToLower(name)) {
	case ".py", ".sh", ".exe", ".bin", ".bat", ".cmd", ".ps1":
		return true
	default:
		return false
	}
}

// DefaultProcessorsDir returns ~/.thlibo/processors. If the home
// dir is unavailable, falls back to a temp location per platform.
func DefaultProcessorsDir() string {
	if d := os.Getenv("THLIBO_PROCESSORS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "thlibo", "processors")
	}
	return filepath.Join(home, ".thlibo", "processors")
}
