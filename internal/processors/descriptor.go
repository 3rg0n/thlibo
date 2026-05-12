package processors

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind distinguishes script processors (pipe through a subprocess)
// from prompt processors (send system prompt + input to the daemon).
type Kind string

const (
	KindScript Kind = "script"
	KindPrompt Kind = "prompt"
)

// Descriptor is the loaded form of a processor.yaml or processor.md
// descriptor. Fields map 1:1 to the spec; anything not in the spec is
// rejected at parse time so typos don't silently go unused.
type Descriptor struct {
	Name        string `yaml:"name"        json:"name"`
	Type        Kind   `yaml:"type"        json:"type"`
	Entry       string `yaml:"entry"       json:"entry,omitempty"` // script only
	Match       string `yaml:"match"       json:"match,omitempty"`
	Description string `yaml:"description" json:"description,omitempty"`

	// Prompt processor knobs (frontmatter fields). All optional;
	// daemon defaults apply when zero.
	Temperature *float64 `yaml:"temperature"    json:"temperature,omitempty"`
	TopP        *float64 `yaml:"top_p"          json:"top_p,omitempty"`
	TopK        *int     `yaml:"top_k"          json:"top_k,omitempty"`
	MaxTokens   *int     `yaml:"max_tokens"     json:"max_tokens,omitempty"`
	Thinking    *bool    `yaml:"thinking"       json:"thinking,omitempty"`
	ThinkBriefly *bool   `yaml:"think_briefly"  json:"think_briefly,omitempty"`

	// SystemPrompt is the markdown body of a processor.md descriptor.
	// Empty for script processors.
	SystemPrompt string `yaml:"-" json:"system_prompt,omitempty"`

	// Origin records where the descriptor came from so error messages
	// and the registry override logic can reference it.
	Origin Origin `yaml:"-" json:"-"`

	// compiledMatch is the regex compiled from Match, ready for
	// fast-path dispatch. nil when Match is empty.
	compiledMatch *regexp.Regexp
}

// Origin records where a descriptor was loaded from. "User" descriptors
// under ~/.thlibo/processors override built-ins with the same name.
type Origin struct {
	Source OriginSource // User or Builtin
	Path   string       // file path (absolute or embed-fs path)
}

type OriginSource int

const (
	OriginBuiltin OriginSource = iota
	OriginUser
)

func (o OriginSource) String() string {
	if o == OriginUser {
		return "user"
	}
	return "builtin"
}

// Match reports whether d's fast-path regex matches input. False when
// no match pattern is configured.
func (d *Descriptor) MatchesFastPath(input string) bool {
	if d.compiledMatch == nil {
		return false
	}
	return d.compiledMatch.MatchString(input)
}

// ParseYAML parses a processor.yaml body.
func ParseYAML(data []byte, origin Origin) (*Descriptor, error) {
	var d Descriptor
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // reject unknown keys
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("processors: parse yaml %s: %w", origin.Path, err)
	}
	d.Origin = origin
	if d.Type == "" {
		d.Type = KindScript // yaml.v3 empty-string zero value
	}
	return &d, validate(&d)
}

// ParseMarkdown parses a processor.md with YAML frontmatter.
// Frontmatter is delimited by --- at the start of the file.
func ParseMarkdown(data []byte, origin Origin) (*Descriptor, error) {
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("processors: split frontmatter %s: %w", origin.Path, err)
	}
	var d Descriptor
	if len(fm) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(fm))
		dec.KnownFields(true)
		if err := dec.Decode(&d); err != nil {
			return nil, fmt.Errorf("processors: parse frontmatter %s: %w", origin.Path, err)
		}
	}
	if d.Type == "" {
		d.Type = KindPrompt // .md implies prompt processor by default
	}
	d.SystemPrompt = string(bytes.TrimSpace(body))
	d.Origin = origin
	return &d, validate(&d)
}

// splitFrontmatter splits a markdown file with optional YAML front-
// matter. Returns (frontmatter bytes, body bytes). A file with no
// leading "---" has empty frontmatter and the whole file as body.
func splitFrontmatter(data []byte) ([]byte, []byte, error) {
	trimmed := bytes.TrimLeft(data, "\r\n ")
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return nil, data, nil
	}
	// Skip the opening --- line.
	nl := bytes.IndexByte(trimmed, '\n')
	if nl < 0 {
		return nil, nil, errors.New("malformed frontmatter: no newline after opening ---")
	}
	rest := trimmed[nl+1:]

	// Find closing --- on its own line (after trimming whitespace).
	end := findClosingDelim(rest)
	if end < 0 {
		return nil, nil, errors.New("malformed frontmatter: no closing ---")
	}
	fm := rest[:end]
	// body starts after the line containing the closing ---.
	bodyStart := end
	if nl2 := bytes.IndexByte(rest[bodyStart:], '\n'); nl2 >= 0 {
		bodyStart += nl2 + 1
	} else {
		bodyStart = len(rest)
	}
	return fm, rest[bodyStart:], nil
}

func findClosingDelim(buf []byte) int {
	// Walk lines.
	i := 0
	for i < len(buf) {
		lineEnd := bytes.IndexByte(buf[i:], '\n')
		var line []byte
		if lineEnd < 0 {
			line = buf[i:]
		} else {
			line = buf[i : i+lineEnd]
		}
		if bytes.Equal(bytes.TrimSpace(line), []byte("---")) {
			return i
		}
		if lineEnd < 0 {
			return -1
		}
		i += lineEnd + 1
	}
	return -1
}

func validate(d *Descriptor) error {
	if d.Name == "" {
		return fmt.Errorf("processor at %s: name is required", d.Origin.Path)
	}
	if !isValidName(d.Name) {
		return fmt.Errorf("processor at %s: name %q must be kebab-case, 1-63 chars", d.Origin.Path, d.Name)
	}
	switch d.Type {
	case KindScript:
		if d.Entry == "" {
			return fmt.Errorf("processor %s: script type requires entry", d.Name)
		}
		if strings.ContainsAny(d.Entry, `/\`) {
			return fmt.Errorf("processor %s: entry %q must be a plain filename, not a path", d.Name, d.Entry)
		}
	case KindPrompt:
		if d.SystemPrompt == "" {
			return fmt.Errorf("processor %s: prompt type requires a non-empty body", d.Name)
		}
	default:
		return fmt.Errorf("processor %s: unknown type %q", d.Name, d.Type)
	}
	if d.Match != "" {
		re, err := regexp.Compile(d.Match)
		if err != nil {
			return fmt.Errorf("processor %s: match regex: %w", d.Name, err)
		}
		d.compiledMatch = re
	}
	return nil
}

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func isValidName(s string) bool { return nameRE.MatchString(s) }

// EntryCommand resolves the interpreter + arguments that run the
// script entry file per spec §"Entry file execution":
//
//	.py         -> python3 <path>
//	.sh         -> bash <path>
//	.exe, .bin  -> direct exec
//
// Unknown extensions return an error so a typo doesn't silently run
// the wrong interpreter.
func (d *Descriptor) EntryCommand(dir string) (string, []string, error) {
	if d.Type != KindScript {
		return "", nil, fmt.Errorf("processor %s: not a script processor", d.Name)
	}
	path := filepath.Join(dir, d.Entry)
	ext := strings.ToLower(filepath.Ext(d.Entry))
	switch ext {
	case ".py":
		return "python3", []string{path}, nil
	case ".sh":
		return "bash", []string{path}, nil
	case ".exe", ".bin":
		return path, nil, nil
	default:
		return "", nil, fmt.Errorf("processor %s: unsupported entry extension %q", d.Name, ext)
	}
}

// FsReader is satisfied by both os (via os.DirFS / os.ReadDir) and
// embed.FS, so the scanner can load from disk or from embedded
// built-ins with the same code path.
type FsReader interface {
	fs.ReadDirFS
	fs.ReadFileFS
}
