//go:build darwin

package install

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type darwinInstaller struct {
	dir string
}

func newDarwinInstaller() (Installer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &darwinInstaller{dir: filepath.Join(home, "Library", "LaunchAgents")}, nil
}

func (d *darwinInstaller) Mechanism() string { return "LaunchAgent" }

// launchAgent is the minimal plist shape launchd accepts. We build
// XML manually to keep the dependency footprint tiny — no plist
// package needed, and the output is stable across launchd versions.
type launchAgent struct {
	XMLName          xml.Name `xml:"plist"`
	Version          string   `xml:"version,attr"`
	Dict             dictNode `xml:"dict"`
}

type dictNode struct {
	Entries []kvNode `xml:",any"`
}

type kvNode struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

func (d *darwinInstaller) Install(spec AutostartSpec) error {
	if err := os.MkdirAll(d.dir, 0o750); err != nil {
		return err
	}
	plist := d.plistXML(spec)
	path := filepath.Join(d.dir, spec.Name+".plist")
	if err := os.WriteFile(path, []byte(plist), 0o600); err != nil {
		return err
	}
	// Load into current session. `launchctl bootstrap gui/$UID` is
	// the modern form; bootstrap is idempotent so re-install works.
	uid := fmt.Sprintf("gui/%d", os.Getuid())
	// #nosec G204 -- uid is formatted from os.Getuid, path is an
	// installer-derived plist location. Neither is user input.
	_ = exec.Command("launchctl", "bootout", uid, path).Run() // ok to fail
	// #nosec G204 -- see above
	return exec.Command("launchctl", "bootstrap", uid, path).Run()
}

func (d *darwinInstaller) Uninstall(name string) error {
	path := filepath.Join(d.dir, name+".plist")
	uid := fmt.Sprintf("gui/%d", os.Getuid())
	// #nosec G204 -- installer-controlled inputs.
	_ = exec.Command("launchctl", "bootout", uid, path).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (d *darwinInstaller) Status(name string) (bool, error) {
	path := filepath.Join(d.dir, name+".plist")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// plistXML hand-rolls the launchd plist. A bespoke writer is used
// because the cross-platform plist libraries add dependencies for
// a 30-line file.
func (d *darwinInstaller) plistXML(spec AutostartSpec) string {
	var args string
	args += fmt.Sprintf("    <string>%s</string>\n", xmlEscape(spec.DaemonPath))
	for _, a := range spec.Args {
		args += fmt.Sprintf("    <string>%s</string>\n", xmlEscape(a))
	}
	wd := ""
	if spec.WorkingDir != "" {
		wd = fmt.Sprintf("  <key>WorkingDirectory</key>\n  <string>%s</string>\n", xmlEscape(spec.WorkingDir))
	}
	logBlock := ""
	if spec.LogPath != "" {
		logBlock = fmt.Sprintf(
			"  <key>StandardOutPath</key>\n  <string>%s</string>\n"+
				"  <key>StandardErrorPath</key>\n  <string>%s</string>\n",
			xmlEscape(spec.LogPath), xmlEscape(spec.LogPath))
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + xmlEscape(spec.Name) + `</string>
  <key>ProgramArguments</key>
  <array>
` + args + `  </array>
` + wd + logBlock + `  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
</dict>
</plist>
`
}

func xmlEscape(s string) string {
	var out []byte
	for _, b := range []byte(s) {
		switch b {
		case '&':
			out = append(out, []byte("&amp;")...)
		case '<':
			out = append(out, []byte("&lt;")...)
		case '>':
			out = append(out, []byte("&gt;")...)
		case '"':
			out = append(out, []byte("&quot;")...)
		default:
			out = append(out, b)
		}
	}
	return string(out)
}
