//go:build linux

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type linuxInstaller struct {
	unitDir string
}

func newLinuxInstaller() (Installer, error) {
	// XDG_CONFIG_HOME defaults to ~/.config.
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		cfg = filepath.Join(home, ".config")
	}
	return &linuxInstaller{unitDir: filepath.Join(cfg, "systemd", "user")}, nil
}

func (l *linuxInstaller) Mechanism() string { return "systemd user unit" }

func (l *linuxInstaller) Install(spec AutostartSpec) error {
	if err := os.MkdirAll(l.unitDir, 0o750); err != nil {
		return err
	}
	unit := l.unitBody(spec)
	path := filepath.Join(l.unitDir, spec.Name+".service")
	if err := os.WriteFile(path, []byte(unit), 0o600); err != nil {
		return err
	}
	// Reload + enable + start. `daemon-reload` picks up the new file;
	// `enable --now` both sets it to autostart and starts it now.
	// All commands best-effort: if systemd isn't running (e.g. a
	// bare container), we leave the unit file in place so a
	// future session with `systemctl --user` works.
	// #nosec G204 -- spec.Name is an installer-set constant
	// ("cisco.thlibo.daemon") or user-supplied via --autostart-name;
	// either way it's intentional input to systemctl. No shell
	// interpretation happens — exec.Command passes argv directly.
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	// #nosec G204 -- see above
	_ = exec.Command("systemctl", "--user", "enable", "--now", spec.Name+".service").Run()
	return nil
}

func (l *linuxInstaller) Uninstall(name string) error {
	// #nosec G204 -- name is an installer-set identifier; see comment
	// in Install above.
	_ = exec.Command("systemctl", "--user", "disable", "--now", name+".service").Run()
	path := filepath.Join(l.unitDir, name+".service")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func (l *linuxInstaller) Status(name string) (bool, error) {
	path := filepath.Join(l.unitDir, name+".service")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (l *linuxInstaller) unitBody(spec AutostartSpec) string {
	args := make([]string, 0, len(spec.Args)+1)
	args = append(args, systemdEscape(spec.DaemonPath))
	for _, a := range spec.Args {
		args = append(args, systemdEscape(a))
	}
	execStart := strings.Join(args, " ")

	wd := ""
	if spec.WorkingDir != "" {
		wd = "WorkingDirectory=" + spec.WorkingDir + "\n"
	}
	// StartLimit caps restart attempts within a 60s window to the
	// daemon's own MaxRestartAttempts. Without it, systemd would loop
	// every RestartSec seconds indefinitely for a persistently-dying
	// daemon. See THREAT_MODEL.md finding #6.
	//
	// NoNewPrivileges / ProtectSystem / ProtectHome / PrivateDevices
	// are defence-in-depth — the daemon already runs as the user.
	// ~/.thlibo is read-write; everything else under $HOME is read-
	// only, and system directories are fully protected. See finding #14.
	return fmt.Sprintf(`[Unit]
Description=thlibo inference daemon
StartLimitIntervalSec=60
StartLimitBurst=3

[Service]
Type=simple
ExecStart=%s
%sRestart=on-failure
RestartSec=2

NoNewPrivileges=true
PrivateDevices=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%%h/.thlibo

[Install]
WantedBy=default.target
`, execStart, wd)
}

// systemdEscape wraps an argument containing spaces in quotes.
// systemd's ExecStart parses whitespace-separated tokens; anything
// more involved needs a string with no literal spaces, which none
// of our default args have.
func systemdEscape(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}
