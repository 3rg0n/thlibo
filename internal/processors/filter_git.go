package processors

import (
	"regexp"
	"strconv"
	"strings"
)

// git-filter: compress git output for an AI assistant's context.
// Native Go port of processors/git-filter/run.py (ADR 0010). Behaviour
// parity is locked by golden tests against the Python reference.
//
// Non-destructive: a line matching no known pattern is kept. Compresses
// `git status` (branch + changed/untracked paths, drops hint lines),
// `git log`, and `git diff` (per-file summary `diff <file> (+N -M)`,
// hunks + patch bodies dropped). Collapses consecutive blank lines.

func init() { RegisterNative("git-filter", gitFilter) }

var (
	gitHintRE       = regexp.MustCompile("^\\s*\\(use [\"`]git")
	gitDiffHeaderRE = regexp.MustCompile(`^diff --git a/(\S+) b/\S+`)
	gitHunkRE       = regexp.MustCompile(`^@@ `)
)

func gitFilter(raw []byte) []byte {
	lines := splitLines(raw)

	var out []string
	inDiffHunk := false
	haveDiff := false
	var diffFile string
	diffPlus, diffMinus := 0, 0

	flushDiff := func() {
		if haveDiff {
			out = append(out, "diff "+diffFile+" (+"+strconv.Itoa(diffPlus)+" -"+strconv.Itoa(diffMinus)+")")
		}
		haveDiff = false
		diffFile = ""
		diffPlus, diffMinus = 0, 0
		inDiffHunk = false
	}

	for _, line := range lines {
		if gitHintRE.MatchString(line) {
			continue
		}

		if m := gitDiffHeaderRE.FindStringSubmatch(line); m != nil {
			flushDiff()
			haveDiff = true
			diffFile = m[1]
			inDiffHunk = false
			continue
		}

		if haveDiff {
			if gitHunkRE.MatchString(line) {
				inDiffHunk = true
				continue
			}
			if inDiffHunk {
				if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
					diffPlus++
					continue
				}
				if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
					diffMinus++
					continue
				}
				if strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "commit ") {
					flushDiff()
					if m2 := gitDiffHeaderRE.FindStringSubmatch(line); m2 != nil {
						haveDiff = true
						diffFile = m2[1]
						inDiffHunk = false
						continue
					}
					// commit line: diff cleared, fall through to keep it.
				} else {
					// Context lines inside the hunk.
					continue
				}
			} else {
				// Pre-hunk metadata (index, ---, +++, Binary, mode). Drop.
				continue
			}
		}

		// Collapse consecutive blank lines.
		if strings.TrimSpace(line) == "" {
			if len(out) > 0 && out[len(out)-1] == "" {
				continue
			}
			out = append(out, "")
			continue
		}

		out = append(out, line)
	}

	flushDiff()

	// Strip trailing blanks.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}

	if len(out) == 0 {
		return nil
	}
	return []byte(strings.Join(out, "\n") + "\n")
}
