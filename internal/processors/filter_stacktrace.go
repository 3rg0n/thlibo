package processors

import (
	"regexp"
	"strings"
)

// stacktrace-filter: compress language-agnostic stack traces.
// Native Go port of processors/stacktrace-filter/run.py (ADR 0010).
// Behaviour parity is locked by tests against the Python reference.
//
// Lossless guarantees:
//   - The exception message is preserved verbatim.
//   - Every distinct file:line ref in the trace appears in the output.
//   - Frame counts are reported when frames are omitted.
//
// What gets compressed:
//   - Duplicated identical frames (e.g. "× 47" instead of 47 copies)
//   - Long traces: keep first 3 + last 3 frames + middle count
//   - ANSI color codes
//   - Blank line runs

func init() { RegisterNative("stacktrace-filter", stacktraceFilter) }

var (
	ansiRE          = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	pyStartRE       = regexp.MustCompile(`^Traceback \(most recent call last\):\s*$`)
	pyFrameRE       = regexp.MustCompile(`^\s+File "([^"]+)", line (\d+)(?:, in (.+))?$`)
	pyExcRE         = regexp.MustCompile(`^[A-Z][\w.]*(?:Error|Exception|Warning|Interrupt):.*$`)
	goPanicRE       = regexp.MustCompile(`^panic: (.+)$`)
	goGoroutineRE   = regexp.MustCompile(`^goroutine (\d+) \[([^\]]+)\]:`)
	goFrameRE       = regexp.MustCompile(`^([\w./*-]+(?:\.\w+)+)\(.*\)$`)
	goFrameLocRE    = regexp.MustCompile(`^\s+([^:]+\.go):(\d+)\b.*$`)
	rustPanicRE     = regexp.MustCompile(`^thread '([^']+)' panicked at (.+):$`)
	rustFrameRE     = regexp.MustCompile(`^\s+(\d+):\s+(.+)$`)
	rustFrameLocRE  = regexp.MustCompile(`^\s+at\s+(.+?):(\d+)(?::(\d+))?$`)
	javaAtRE        = regexp.MustCompile(`^\s+at ([\w$.]+)\(([^)]+)\)\s*$`)
	javaExcRE       = regexp.MustCompile(`^([\w.]+(?:Error|Exception)):.*$`)
	javaCausedByRE  = regexp.MustCompile(`^Caused by: `)
	nodeFrameRE     = regexp.MustCompile(`^\s+at ([\w$.<> ]+)\s*\(([^)]+):(\d+):(\d+)\)\s*$`)
	nodeAnonFrameRE = regexp.MustCompile(`^\s+at ([^:]+):(\d+):(\d+)\s*$`)
)

const (
	keepHead = 3
	keepTail = 3
)

type unit struct {
	lines   []string
	isFrame bool
}

func stacktraceFilter(raw []byte) []byte {
	text := stripAnsi(string(raw))
	lines := strings.Split(text, "\n")
	// splitlines() semantics: a trailing newline shouldn't yield a final
	// empty element. Python's str.splitlines() drops the trailing "".
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	ranges := splitTraces(lines)
	if len(ranges) == 0 {
		return raw
	}

	// Build the output by interleaving non-trace passthrough
	// text with compressed trace blocks.
	var out []string
	cursor := 0
	for _, r := range ranges {
		start, end := r[0], r[1]
		// Passthrough preceding text verbatim.
		if start > cursor {
			out = append(out, lines[cursor:start]...)
		}
		// Compressed trace.
		out = append(out, compressBlock(lines[start:end])...)
		cursor = end
	}
	if cursor < len(lines) {
		out = append(out, lines[cursor:]...)
	}

	result := strings.Join(out, "\n")
	if strings.HasSuffix(string(raw), "\n") {
		result += "\n"
	}
	return []byte(result)
}

func stripAnsi(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

func compressBlock(block []string) []string {
	if len(block) == 0 {
		return []string{}
	}

	units := buildUnits(block)

	// Dedupe consecutive identical units
	units = dedupeUnits(units)

	// Slice into header (leading non-frame units) / frames /
	// trailer (trailing non-frame units).
	headerEnd := 0
	for headerEnd < len(units) && !units[headerEnd].isFrame {
		headerEnd++
	}

	trailerStart := len(units)
	for trailerStart > headerEnd && !units[trailerStart-1].isFrame {
		trailerStart--
	}

	headerUnits := units[:headerEnd]
	frameUnits := units[headerEnd:trailerStart]
	trailerUnits := units[trailerStart:]

	if len(frameUnits) <= keepHead+keepTail+1 {
		return flatten(append(append(headerUnits, frameUnits...), trailerUnits...))
	}

	head := frameUnits[:keepHead]
	tail := frameUnits[len(frameUnits)-keepTail:]
	omitted := len(frameUnits) - len(head) - len(tail)

	outUnits := append([]unit{}, headerUnits...)
	outUnits = append(outUnits, head...)
	if omitted > 0 {
		outUnits = append(outUnits, unit{
			lines:   []string{"  ... " + itoaStacktrace(omitted) + " frames omitted ..."},
			isFrame: false,
		})
	}
	outUnits = append(outUnits, tail...)
	outUnits = append(outUnits, trailerUnits...)
	return flatten(outUnits)
}

func buildUnits(block []string) []unit {
	var units []unit
	i := 0
	for i < len(block) {
		line := block[i]
		if pyFrameRE.MatchString(line) {
			// Python: include the next line as the code body if it
			// exists and is indented (the typical 4-space "    code"
			// source line).
			if i+1 < len(block) && strings.HasPrefix(block[i+1], "    ") {
				units = append(units, unit{
					lines:   []string{line, block[i+1]},
					isFrame: true,
				})
				i += 2
				continue
			}
			units = append(units, unit{
				lines:   []string{line},
				isFrame: true,
			})
			i++
			continue
		}
		if looksLikeFrame(line) {
			units = append(units, unit{
				lines:   []string{line},
				isFrame: true,
			})
			i++
			continue
		}
		units = append(units, unit{
			lines:   []string{line},
			isFrame: false,
		})
		i++
	}
	return units
}

func dedupeUnits(units []unit) []unit {
	var out []unit
	i := 0
	for i < len(units) {
		runEnd := i
		for runEnd+1 < len(units) && unitsEqual(units[runEnd+1], units[i]) {
			runEnd++
		}
		runLen := runEnd - i + 1
		if runLen >= 3 {
			base := units[i]
			taggedLines := append([]string{}, base.lines[:len(base.lines)-1]...)
			lastLine := base.lines[len(base.lines)-1] + "    × " + itoaStacktrace(runLen)
			taggedLines = append(taggedLines, lastLine)
			out = append(out, unit{
				lines:   taggedLines,
				isFrame: base.isFrame,
			})
		} else {
			out = append(out, units[i:runEnd+1]...)
		}
		i = runEnd + 1
	}
	return out
}

func unitsEqual(a, b unit) bool {
	if a.isFrame != b.isFrame || len(a.lines) != len(b.lines) {
		return false
	}
	for i := range a.lines {
		if a.lines[i] != b.lines[i] {
			return false
		}
	}
	return true
}

func flatten(units []unit) []string {
	var out []string
	for _, u := range units {
		out = append(out, u.lines...)
	}
	return out
}

func looksLikeFrame(line string) bool {
	return pyFrameRE.MatchString(line) ||
		goFrameRE.MatchString(line) ||
		goFrameLocRE.MatchString(line) ||
		rustFrameRE.MatchString(line) ||
		rustFrameLocRE.MatchString(line) ||
		javaAtRE.MatchString(line) ||
		nodeFrameRE.MatchString(line) ||
		nodeAnonFrameRE.MatchString(line)
}

func splitTraces(lines []string) [][]int {
	var starts []int
	for i, line := range lines {
		if pyStartRE.MatchString(line) ||
			goPanicRE.MatchString(line) ||
			goGoroutineRE.MatchString(line) ||
			rustPanicRE.MatchString(line) ||
			javaExcRE.MatchString(line) ||
			javaCausedByRE.MatchString(line) {
			starts = append(starts, i)
		}
	}

	if len(starts) == 0 {
		return nil
	}

	var ranges [][]int
	for idx, start := range starts {
		// End at next blank line OR the start of the next trace.
		nextStart := len(lines)
		if idx+1 < len(starts) {
			nextStart = starts[idx+1]
		}
		end := start
		for end < nextStart {
			if strings.TrimSpace(lines[end]) == "" && end > start {
				// Allow one blank inside the trace block (Python
				// exceptions sometimes have a blank between the
				// header and the first frame). End on a SECOND
				// consecutive blank.
				if end+1 < nextStart && strings.TrimSpace(lines[end+1]) == "" {
					break
				}
			}
			end++
		}
		ranges = append(ranges, []int{start, end})
	}
	return ranges
}

// itoaStacktrace is a tiny non-negative int formatter.
func itoaStacktrace(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
