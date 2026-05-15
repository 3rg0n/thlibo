// Package configcmd implements `thlibo config`.
//
// Walks the user through the ~/.thlibo/config.yaml schema with
// sequential prompts. Each question shows the current value and
// accepts an empty line to keep it. After all questions, prints a
// diff and asks to confirm.
//
// Modes:
//
//	thlibo config              Interactive Q&A then write.
//	thlibo config --show       Print current settings, exit.
//	thlibo config --path       Print the resolved config path, exit.
//	thlibo config --set k=v    Set one field non-interactively.
//	                           Supports nested keys via dot:
//	                             thlibo config --set auto_shorthand_on_write=true
//	thlibo config --reset      Write Defaults() to ~/.thlibo/config.yaml.
package configcmd

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/3rg0n/thlibo/internal/config"
	"gopkg.in/yaml.v3"
)

// Exit codes.
const (
	ExitOK             = 0
	ExitUsage          = 2
	ExitWriteFailed    = 3
	ExitInvalidSetting = 4
	ExitAborted        = 5
)

// Run is the subcommand entry point.
func Run(argv []string) int {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	var (
		show  bool
		path  bool
		set   string
		reset bool
		yes   bool
	)
	fs.BoolVar(&show, "show", false, "print current settings (with source paths) and exit")
	fs.BoolVar(&path, "path", false, "print the resolved config-file path and exit")
	fs.StringVar(&set, "set", "", "set one field non-interactively, k=v form (e.g. auto_shorthand_on_write=true)")
	fs.BoolVar(&reset, "reset", false, "write Defaults() to the config file")
	fs.BoolVar(&yes, "yes", false, "skip the confirmation prompt at the end of interactive mode")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `thlibo config — manage ~/.thlibo/config.yaml

Usage:
  thlibo config              Interactive Q&A.
  thlibo config --show       Print current settings.
  thlibo config --path       Print the config-file path.
  thlibo config --set k=v    Set one field non-interactively.
  thlibo config --reset      Reset to defaults.

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return ExitUsage
	}

	cfgPath, err := config.Path()
	if err != nil {
		fmt.Fprintln(os.Stderr, "thlibo config: cannot resolve home dir:", err)
		return ExitWriteFailed
	}

	switch {
	case path:
		fmt.Println(cfgPath)
		return ExitOK
	case show:
		return runShow(cfgPath)
	case set != "":
		return runSet(cfgPath, set)
	case reset:
		return runReset(cfgPath, yes)
	default:
		return runInteractive(cfgPath, os.Stdin, os.Stdout, yes)
	}
}

// runShow prints the active config + the path it was loaded from.
func runShow(cfgPath string) int {
	cfg := config.Load()
	fmt.Println("# Active thlibo config")
	fmt.Println("# Source:", cfgPath)
	if _, err := os.Stat(cfgPath); err != nil {
		fmt.Println("# (file does not exist; showing built-in defaults)")
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "thlibo config: marshal:", err)
		return ExitWriteFailed
	}
	fmt.Print(string(out))
	return ExitOK
}

// runSet applies a single key=value pair non-interactively. Used by
// the installer (so `thlibo install --enable-auto-shorthand` could
// flip the toggle without dropping into Q&A) and by anyone scripting
// the setup. Supported keys are the flat field names plus the
// `auto_shorthand_paths.add` / `.remove` pseudo-keys for list
// mutation.
func runSet(cfgPath, kv string) int {
	parts := strings.SplitN(kv, "=", 2)
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "thlibo config: --set expects key=value (got", kv+")")
		return ExitUsage
	}
	key, val := parts[0], parts[1]

	cfg := config.Load()
	if err := applySet(&cfg, key, val); err != nil {
		fmt.Fprintln(os.Stderr, "thlibo config: invalid setting:", err)
		return ExitInvalidSetting
	}
	if err := writeConfig(cfgPath, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "thlibo config: write:", err)
		return ExitWriteFailed
	}
	fmt.Printf("set %s=%s in %s\n", key, val, cfgPath)
	return ExitOK
}

// runReset writes Defaults() to the config file, after confirmation
// unless --yes was passed.
func runReset(cfgPath string, yes bool) int {
	if !yes {
		fmt.Printf("Reset config at %s to defaults? [y/N]: ", cfgPath)
		reader := bufio.NewReader(os.Stdin)
		ans, _ := reader.ReadString('\n')
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans != "y" && ans != "yes" {
			fmt.Println("aborted")
			return ExitAborted
		}
	}
	if err := writeConfig(cfgPath, config.Defaults()); err != nil {
		fmt.Fprintln(os.Stderr, "thlibo config: write:", err)
		return ExitWriteFailed
	}
	fmt.Printf("reset %s to defaults\n", cfgPath)
	return ExitOK
}

// runInteractive walks the user through one question per
// configurable field. Exported as a function rather than a method
// so it's easy to drive from tests with custom input streams.
func runInteractive(cfgPath string, in io.Reader, out io.Writer, yes bool) int {
	cfg := config.Load()
	original := cfg

	fmt.Fprintln(out, "thlibo config — interactive setup")
	fmt.Fprintln(out, "Press Enter to keep the current value. Type 'q' to abort.")
	fmt.Fprintln(out)

	reader := bufio.NewReader(in)

	prompts := configPrompts(&cfg)
	for _, p := range prompts {
		if !p.ask(reader, out) {
			fmt.Fprintln(out, "aborted; nothing written.")
			return ExitAborted
		}
	}

	// Diff what changed.
	changed := diff(original, cfg)
	if len(changed) == 0 {
		fmt.Fprintln(out, "\nno changes.")
		return ExitOK
	}

	fmt.Fprintln(out, "\nChanges:")
	for _, line := range changed {
		fmt.Fprintln(out, " ", line)
	}

	if !yes {
		fmt.Fprintf(out, "\nWrite to %s? [Y/n]: ", cfgPath)
		ans, _ := reader.ReadString('\n')
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans == "n" || ans == "no" {
			fmt.Fprintln(out, "aborted")
			return ExitAborted
		}
	}

	if err := writeConfig(cfgPath, cfg); err != nil {
		fmt.Fprintln(out, "thlibo config: write:", err)
		return ExitWriteFailed
	}
	fmt.Fprintf(out, "wrote %s\n", cfgPath)
	return ExitOK
}

// prompt is one Q in the interactive walk. ask returns false when
// the user typed 'q' to abort; true otherwise (including when they
// hit Enter to keep the current value).
type prompt struct {
	question string
	current  string
	help     string
	apply    func(string) error
}

func (p *prompt) ask(r *bufio.Reader, out io.Writer) bool {
	fmt.Fprintln(out, p.help)
	fmt.Fprintf(out, "  %s [%s]: ", p.question, p.current)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return false
	}
	line = strings.TrimSpace(line)
	if line == "q" || line == "Q" {
		return false
	}
	if line == "" {
		return true // keep current
	}
	if err := p.apply(line); err != nil {
		fmt.Fprintln(out, "  → invalid:", err)
		// Re-ask the same question once. Avoids infinite loops by
		// only retrying once; second invalid answer accepts as
		// "abort the field" (current value stays).
		return p.ask(r, out)
	}
	return true
}

// configPrompts builds the question list for the interactive walk.
// One per Config field that's user-meaningful. Order matters —
// auto-shorthand-on-write comes first because it's the master
// switch for the whole feature; other fields are only relevant
// when it's on.
func configPrompts(cfg *config.Config) []prompt {
	return []prompt{
		{
			question: "Enable auto-shorthand on Write/Edit (true/false)",
			current:  fmt.Sprintf("%v", cfg.AutoShorthandOnWrite),
			help: "When true, the Write/Edit PreToolUse hook runs `thlibo shorthand`\n" +
				"on the content before it lands on disk for matched paths. Off by\n" +
				"default. Eval-gated; failure = passthrough.",
			apply: func(s string) error {
				v, err := strconv.ParseBool(s)
				if err != nil {
					return fmt.Errorf("expected true/false, got %q", s)
				}
				cfg.AutoShorthandOnWrite = v
				return nil
			},
		},
		{
			question: "Auto-shorthand path globs (comma-separated)",
			current:  strings.Join(cfg.AutoShorthandPaths, ","),
			help: "File paths matching any of these globs are eligible for the\n" +
				"auto-rewrite when the toggle above is on. Use **/foo for any-\n" +
				"depth match.",
			apply: func(s string) error {
				parts := strings.Split(s, ",")
				cleaned := make([]string, 0, len(parts))
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p == "" {
						continue
					}
					cleaned = append(cleaned, p)
				}
				if len(cleaned) == 0 {
					return fmt.Errorf("at least one glob required")
				}
				cfg.AutoShorthandPaths = cleaned
				return nil
			},
		},
		{
			question: "Minimum file size (bytes) to trigger auto-shorthand",
			current:  strconv.Itoa(cfg.AutoShorthandMinBytes),
			help: "Files smaller than this go through unchanged. Below ~500 bytes\n" +
				"the round-trip cost rarely beats the savings.",
			apply: func(s string) error {
				n, err := strconv.Atoi(s)
				if err != nil {
					return fmt.Errorf("expected an integer, got %q", s)
				}
				if n < 0 {
					return fmt.Errorf("must be ≥ 0")
				}
				cfg.AutoShorthandMinBytes = n
				return nil
			},
		},
		{
			question: "Enable YAML-aware mode for prompts/*.yaml (true/false)",
			current:  fmt.Sprintf("%v", cfg.AutoShorthandYAMLProse),
			help: "When true AND the file is YAML, walk the AST and only rewrite\n" +
				"prose-shaped scalars. Keys, lists, and structural fields\n" +
				"(allowed_tools, name, version, etc.) stay byte-identical.",
			apply: func(s string) error {
				v, err := strconv.ParseBool(s)
				if err != nil {
					return fmt.Errorf("expected true/false, got %q", s)
				}
				cfg.AutoShorthandYAMLProse = v
				return nil
			},
		},
	}
}

// applySet handles the --set form. Same field set as the
// interactive walk, but each maps a flat key string to a setter.
func applySet(cfg *config.Config, key, val string) error {
	switch key {
	case "auto_shorthand_on_write":
		v, err := strconv.ParseBool(val)
		if err != nil {
			return err
		}
		cfg.AutoShorthandOnWrite = v
	case "auto_shorthand_min_bytes":
		n, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		if n < 0 {
			return fmt.Errorf("must be ≥ 0")
		}
		cfg.AutoShorthandMinBytes = n
	case "auto_shorthand_yaml_prose":
		v, err := strconv.ParseBool(val)
		if err != nil {
			return err
		}
		cfg.AutoShorthandYAMLProse = v
	case "auto_shorthand_paths":
		parts := strings.Split(val, ",")
		cleaned := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			cleaned = append(cleaned, p)
		}
		if len(cleaned) == 0 {
			return fmt.Errorf("at least one glob required")
		}
		cfg.AutoShorthandPaths = cleaned
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

// diff returns human-readable lines describing what changed between
// before and after. Used by the interactive walk's confirmation
// step.
func diff(before, after config.Config) []string {
	var out []string
	if before.AutoShorthandOnWrite != after.AutoShorthandOnWrite {
		out = append(out, fmt.Sprintf("auto_shorthand_on_write: %v → %v",
			before.AutoShorthandOnWrite, after.AutoShorthandOnWrite))
	}
	if before.AutoShorthandMinBytes != after.AutoShorthandMinBytes {
		out = append(out, fmt.Sprintf("auto_shorthand_min_bytes: %d → %d",
			before.AutoShorthandMinBytes, after.AutoShorthandMinBytes))
	}
	if before.AutoShorthandYAMLProse != after.AutoShorthandYAMLProse {
		out = append(out, fmt.Sprintf("auto_shorthand_yaml_prose: %v → %v",
			before.AutoShorthandYAMLProse, after.AutoShorthandYAMLProse))
	}
	if !stringSliceEqual(before.AutoShorthandPaths, after.AutoShorthandPaths) {
		out = append(out, fmt.Sprintf("auto_shorthand_paths: %d → %d entries",
			len(before.AutoShorthandPaths), len(after.AutoShorthandPaths)))
	}
	return out
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// writeConfig serialises cfg as YAML and writes it to path,
// creating the parent directory if needed.
func writeConfig(path string, cfg config.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	header := []byte("# thlibo config — managed by `thlibo config`\n# Edit by hand or run `thlibo config` to walk through the schema.\n\n")
	return os.WriteFile(path, append(header, body...), 0o600)
}
