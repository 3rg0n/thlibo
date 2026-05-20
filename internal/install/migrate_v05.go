// v0.5.x → v0.6.0 migration. The v0.5 line shipped an embedded
// thlibod daemon plus a llamafile engine plus an autostart entry,
// all of which are gone in v0.6.0 (inferd owns inference now).
// Existing v0.5 installs need their daemon stopped + removed and
// their model moved to the shared model store before the v0.6
// thlibo binary takes over.
//
// This package does the surgery. It is invoked by `thlibo install`
// at the start of the install flow and is idempotent: running it
// twice is a no-op the second time. It deliberately leaves
// ~/.thlibo/{processors,hooks,config.yaml,state} alone — those are
// still load-bearing in v0.6.

package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// MigrateResult is the report MigrateFromV05 returns. Empty
// (zero-valued) means nothing to migrate; populated means the
// caller should print the actions taken so the user knows what
// changed.
type MigrateResult struct {
	StoppedAutostart   bool   // a v0.5 daemon autostart entry was stopped + removed
	RemovedDaemonBin   bool   // thlibod binary was deleted
	RemovedEngineBin   bool   // thlibo-engine (llamafile) was deleted
	ModelMovedFrom     string // old GGUF path; empty if no move happened
	ModelMovedTo       string // new GGUF path
	RemovedModelsDir   bool   // ~/.thlibo/models/ was empty after move and deleted
	RemovedLogsDir     bool   // ~/.thlibo/logs/ was deleted
	Notes              []string
}

// HasWork reports whether any v0.5.x artefacts were found and
// acted on. Used to gate the "migrated from v0.5" announcement
// in the install output.
func (r MigrateResult) HasWork() bool {
	return r.StoppedAutostart || r.RemovedDaemonBin || r.RemovedEngineBin ||
		r.ModelMovedFrom != "" || r.RemovedModelsDir || r.RemovedLogsDir
}

// MigrateFromV05 runs the v0.5.x exorcism. Safe to call on a host
// that never had v0.5 installed (no-op) and on a host that has
// already been migrated (no-op). Errors are best-effort: each step
// records its own outcome but does not abort the rest.
//
// The caller is responsible for reporting the result. This package
// stays log-free so the installer can format the messages in line
// with its other output.
func MigrateFromV05() (MigrateResult, error) {
	var r MigrateResult

	home, err := os.UserHomeDir()
	if err != nil {
		return r, fmt.Errorf("migrate: cannot resolve home dir: %w", err)
	}

	// 1. Stop + remove v0.5 autostart entry. Best-effort; log a
	//    note if we couldn't but don't fail the whole migration.
	if note, ok := stopV05Autostart(); note != "" {
		if ok {
			r.StoppedAutostart = true
		}
		r.Notes = append(r.Notes, note)
	}

	// 2. Delete the v0.5 daemon binary if present.
	thlibod := filepath.Join(home, ".local", "bin", thlibodBinName())
	if removed, _ := tryRemove(thlibod); removed {
		r.RemovedDaemonBin = true
	}
	// Windows variant: %LOCALAPPDATA%\thlibo\bin\thlibod.exe
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
			alt := filepath.Join(appData, "thlibo", "bin", "thlibod.exe")
			if removed, _ := tryRemove(alt); removed {
				r.RemovedDaemonBin = true
			}
		}
	}

	// 3. Delete the llamafile engine binary if present.
	for _, p := range engineBinPaths(home) {
		if removed, _ := tryRemove(p); removed {
			r.RemovedEngineBin = true
		}
	}

	// 4. Move the model GGUF to the shared store. Skipped silently
	//    if the old path doesn't exist (nothing to move) or if the
	//    new path already has a non-empty file (already migrated;
	//    avoid clobbering).
	oldModelsDir := filepath.Join(home, ".thlibo", "models")
	if entries, err := os.ReadDir(oldModelsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(strings.ToLower(name), ".gguf") &&
				!strings.HasSuffix(strings.ToLower(name), ".safetensors") {
				continue
			}
			oldPath := filepath.Join(oldModelsDir, name)
			newPath := filepath.Join(SharedModelsDir(), name)

			// Idempotency: if the new path already exists with the
			// same size, drop the old one (already moved on a prior
			// run that crashed mid-cleanup).
			if alreadyAtNew(oldPath, newPath) {
				_ = os.Remove(oldPath)
				continue
			}

			if err := moveAcrossFS(oldPath, newPath); err != nil {
				r.Notes = append(r.Notes,
					fmt.Sprintf("could not move %s to %s: %v", oldPath, newPath, err))
				continue
			}
			r.ModelMovedFrom = oldPath
			r.ModelMovedTo = newPath
		}
		// 5. Empty old models dir? Nuke it.
		if remaining, _ := os.ReadDir(oldModelsDir); len(remaining) == 0 {
			if err := os.Remove(oldModelsDir); err == nil {
				r.RemovedModelsDir = true
			}
		}
	}

	// 6. Daemon log dir is exclusively for thlibod; remove it.
	logsDir := filepath.Join(home, ".thlibo", "logs")
	if entries, err := os.ReadDir(logsDir); err == nil {
		// Only consider it daemon-owned if it contains thlibod-shaped
		// files. An empty or unrelated logs dir is left alone.
		removeLogs := false
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "thlibod") {
				removeLogs = true
				break
			}
		}
		if removeLogs {
			_ = os.RemoveAll(logsDir) // #nosec G104 -- best-effort cleanup
			r.RemovedLogsDir = true
		}
	}

	// 7. ~/.thlibo/run/ is the lock+socket dir from v0.5.4. Now
	//    inferd owns runtime sockets, so the old dir is dead.
	runDir := filepath.Join(home, ".thlibo", "run")
	_ = os.RemoveAll(runDir) // #nosec G104 -- best-effort cleanup

	return r, nil
}

// SharedModelsDir resolves the per-platform shared model store
// location described in .plan/spec.issue.md §3.1.
func SharedModelsDir() string {
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		if d := os.Getenv("XDG_DATA_HOME"); d != "" {
			return filepath.Join(d, "models")
		}
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, ".local", "share", "models")
		}
	case "darwin":
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, "Library", "Application Support", "models")
		}
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "models")
		}
	}
	// Final fallback: alongside the user's home dir.
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "share", "models")
	}
	return filepath.Join(os.TempDir(), "models")
}

// stopV05Autostart removes the v0.5 systemd / launchd / Startup
// entry. Returns a human-readable note (or empty if there was
// nothing to do) and whether the action succeeded.
func stopV05Autostart() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	switch runtime.GOOS {
	case "linux":
		unit := filepath.Join(home, ".config", "systemd", "user", "cisco.thlibo.daemon.service")
		if _, err := os.Stat(unit); err != nil {
			return "", false
		}
		// Best-effort: stop + disable + remove. If systemctl isn't on
		// PATH (containers without systemd), we still delete the
		// unit file so a future logind session doesn't try to load
		// it.
		// #nosec G204 -- fixed argv, no user input
		_ = exec.Command("systemctl", "--user", "stop", "cisco.thlibo.daemon.service").Run()
		// #nosec G204 -- fixed argv, no user input
		_ = exec.Command("systemctl", "--user", "disable", "cisco.thlibo.daemon.service").Run()
		if err := os.Remove(unit); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "could not remove " + unit + ": " + err.Error(), false
		}
		return "stopped + removed cisco.thlibo.daemon.service (systemd --user)", true
	case "darwin":
		plist := filepath.Join(home, "Library", "LaunchAgents", "cisco.thlibo.daemon.plist")
		if _, err := os.Stat(plist); err != nil {
			return "", false
		}
		// `launchctl bootout` requires the gui/<uid>/<label> form.
		// Detect uid from $UID or os.Getuid; gracefully degrade if
		// launchctl missing or already booted out.
		uid := fmt.Sprintf("%d", os.Getuid())
		// #nosec G204 -- fixed argv, no user input
		_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/cisco.thlibo.daemon").Run()
		if err := os.Remove(plist); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "could not remove " + plist + ": " + err.Error(), false
		}
		return "removed LaunchAgent cisco.thlibo.daemon", true
	case "windows":
		startup := os.Getenv("APPDATA")
		if startup == "" {
			return "", false
		}
		shim := filepath.Join(startup, "Microsoft", "Windows", "Start Menu", "Programs",
			"Startup", "cisco.thlibo.daemon.cmd")
		// #nosec G703 -- shim is %APPDATA% + literal subpath; not user input
		if _, err := os.Stat(shim); err != nil {
			return "", false
		}
		// #nosec G703 -- shim as above
		if err := os.Remove(shim); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "could not remove " + shim + ": " + err.Error(), false
		}
		return "removed Startup shim cisco.thlibo.daemon.cmd", true
	}
	return "", false
}

// thlibodBinName returns the platform-correct daemon binary name.
func thlibodBinName() string {
	if runtime.GOOS == "windows" {
		return "thlibod.exe"
	}
	return "thlibod"
}

// engineBinPaths returns the candidate paths the v0.5 engine may
// be at. v0.5.2+ moved the engine to %LOCALAPPDATA%\thlibo\bin on
// Windows; earlier versions kept it in ~/.local/bin. Check both.
func engineBinPaths(home string) []string {
	exe := "thlibo-engine"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	paths := []string{filepath.Join(home, ".local", "bin", exe)}
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
			paths = append(paths, filepath.Join(appData, "thlibo", "bin", exe))
		}
	}
	return paths
}

// tryRemove deletes path if it exists. Returns (removed, error).
// "Not exist" is not an error.
//
// All callers pass paths derived from os.UserHomeDir + literal
// subpaths (see callers in MigrateFromV05); none are user input.
// gosec's taint analysis can't follow that through; hence the
// G703 annotations on the file ops below.
func tryRemove(path string) (bool, error) {
	// #nosec G703 -- caller-controlled $HOME-rooted path
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	// #nosec G703 -- path as above
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

// alreadyAtNew reports whether newPath has a file with the same
// size as oldPath (cheap idempotency check). We don't re-verify the
// SHA — that's inferd's job at load time.
func alreadyAtNew(oldPath, newPath string) bool {
	oldInfo, err1 := os.Stat(oldPath)
	newInfo, err2 := os.Stat(newPath)
	if err1 != nil || err2 != nil {
		return false
	}
	return oldInfo.Size() == newInfo.Size() && newInfo.Size() > 0
}

// moveAcrossFS moves src to dst, falling back to copy+delete if
// they're on different filesystems (rename(2) returns EXDEV).
// Creates the destination's parent dir if missing.
func moveAcrossFS(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Fall back to copy + delete.
	in, err := os.Open(src) // #nosec G304 -- caller-supplied path within ~/.thlibo/models
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304
	if err != nil {
		return fmt.Errorf("open dst: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("close dst: %w", err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove src after copy: %w", err)
	}
	return nil
}
