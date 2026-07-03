package processors

import (
	"regexp"
	"strings"
)

// pytest-filter: compress pytest stdout for an AI assistant.
// Native Go port of processors/pytest-filter/run.py (ADR 0010). Behaviour
// parity is locked by golden tests against the Python reference.
//
// Lossless for parts an AI cares about (failures, errors, summary, tracebacks).
// Drops: progress dot streams, environment info, per-test capture, blank-line runs.

func init() { RegisterNative("pytest-filter", pytestFilter) }

var (
	pytestAnsiRE        = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	sectionRE           = regexp.MustCompile(`^=+ (.+?) =+\s*$`)
	collectedRE         = regexp.MustCompile(`^collected (\d+) items?`)
	progressLineRE      = regexp.MustCompile(`^[\w./_-]+\.py [.FEsxXp]+\s+\[\s*\d+%\s*\]\s*$`)
	shortSummaryRE      = regexp.MustCompile(`^=+ (\d+ (?:passed|failed|error|skipped|warning|deselected)[^=]*) =+\s*$`)
	passedFailedLineRE  = regexp.MustCompile(`^(\d+ (?:passed|failed|error|errors|skipped|warning)s?[^=]*) in [\d.]+s`)
)

var keepSections = map[string]bool{
	"FAILURES":              true,
	"ERRORS":                true,
	"short test summary info": true,
}

const warningsSection = "warnings summary"

func stripANSI(s string) string {
	return pytestAnsiRE.ReplaceAllString(s, "")
}

// sectionMap maps section start-line indices to section names.
type sectionMap struct {
	lookup map[int]string   // line index -> section name
	starts []int            // sorted start indices
	total  int              // total line count
}

func (sm *sectionMap) contains(idx int) bool {
	_, ok := sm.lookup[idx]
	return ok
}

func (sm *sectionMap) get(idx int) string {
	return sm.lookup[idx]
}

func (sm *sectionMap) getEnd(startIdx int) int {
	// Return the line index just past the end of the section.
	// Section ends at the next section boundary or end-of-input.
	for _, s := range sm.starts {
		if s > startIdx {
			return s
		}
	}
	return sm.total
}

func findSections(lines []string) *sectionMap {
	sm := &sectionMap{
		lookup: make(map[int]string),
		total:  len(lines),
	}
	for i, line := range lines {
		m := sectionRE.FindStringSubmatch(line)
		if m != nil {
			name := strings.TrimSpace(m[1])
			sm.lookup[i] = name
			sm.starts = append(sm.starts, i)
		}
	}
	return sm
}

func hasFailures(lines []string, sections *sectionMap) bool {
	for i, line := range lines {
		if i > 0 && sections.contains(i) {
			name := sections.get(i)
			if name == "FAILURES" {
				return true
			}
		}
		m := shortSummaryRE.FindStringSubmatch(line)
		if m != nil && strings.Contains(m[1], "failed") {
			return true
		}
	}
	return false
}

func pytestFilter(raw []byte) []byte {
	// Strip ANSI codes.
	text := stripANSI(string(raw))
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	// Drop trailing empty element from split (Python splitlines semantics).
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	// Identify section boundaries.
	sections := findSections(lines)
	if len(sections.lookup) == 0 {
		// Doesn't look like pytest output — pass through.
		return raw
	}

	var out []string
	inProgress := false
	lastBlank := false

	for i := 0; i < len(lines); {
		line := lines[i]

		// Section boundary?
		if sections.contains(i) {
			sectionName := sections.get(i)
			end := sections.getEnd(i)

			if keepSections[sectionName] {
				out = append(out, lines[i:end]...)
				inProgress = false
				lastBlank = false
				i = end
				continue
			}

			if sectionName == warningsSection {
				// Keep only if FAILURES exist.
				if hasFailures(lines, sections) {
					out = append(out, lines[i:end]...)
				}
				i = end
				lastBlank = false
				continue
			}

			if strings.HasPrefix(sectionName, "test session starts") {
				// Keep the section header; suppress env info until `collected N`.
				out = append(out, line)
				lastBlank = false
				i++
				for i < len(lines) && !collectedRE.MatchString(lines[i]) {
					i++
				}
				continue
			}

			// Unknown section — keep as-is.
			out = append(out, lines[i:end]...)
			i = end
			continue
		}

		// Within in-progress dot stream, drop progress lines.
		if collectedRE.MatchString(line) {
			out = append(out, line)
			inProgress = true
			lastBlank = false
			i++
			continue
		}

		if inProgress && progressLineRE.MatchString(line) {
			// Drop the per-file dot line.
			i++
			continue
		}

		// Compact blank-line runs.
		if strings.TrimSpace(line) == "" {
			if !lastBlank {
				out = append(out, line)
			}
			lastBlank = true
			i++
			continue
		}
		lastBlank = false

		// Tail summary line — always keep.
		if passedFailedLineRE.MatchString(line) {
			out = append(out, line)
			i++
			continue
		}

		out = append(out, line)
		i++
	}

	result := strings.Join(out, "\n")
	// Match Python: trailing newline only if input had one.
	if strings.HasSuffix(string(raw), "\n") {
		result += "\n"
	}

	return []byte(result)
}
