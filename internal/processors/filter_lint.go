package processors

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// lint-filter: distill verbose lint output for an AI client. Native Go
// port of processors/lint-filter/run.py (ADR 0010). Behaviour parity is
// locked by golden tests against the Python reference.
//
// Output: TSV, one row per finding — sevLetter \t file:line[:col] \t
// rule \t message; an optional help row (`-` in the sev column) follows
// a finding that carried a `help:` suggestion. Verbose multi-line shapes
// (rustc/clippy/ruff/gcc/eslint-stylish) are distilled; terse
// single-line shapes are parsed line-by-line. Monotonic: if the
// distilled output isn't smaller than the input, the input is returned
// verbatim (matches the Python, which returns raw in that case).

func init() { RegisterNative("lint-filter", lintFilter) }

var lintANSIRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func maxPerRuleFromEnv() int {
	s := os.Getenv("LINT_MAX_PER_RULE")
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

type finding struct {
	kind string
	sev  string
	file string
	line int
	col  int
	rule string
	msg  string
	help string
}

var lintSevLetter = map[string]string{
	"error": "E", "warning": "W", "info": "I", "note": "N",
	"style": "S", "convention": "C", "refactor": "R", "help": "H",
}

var lintSevRank = map[string]int{
	"error": 4, "warning": 3, "info": 2, "style": 1, "note": 1,
	"convention": 1, "refactor": 1, "help": 0,
}

var lintSevNormal = map[string]string{
	"fatal": "error", "panic": "error", "crit": "error", "critical": "error",
	"err": "error", "e": "error", "f": "error",
	"warn": "warning", "w": "warning",
	"n": "note", "i": "info", "s": "style",
	"c": "convention", "r": "refactor",
	"high": "error", "medium": "warning", "low": "info",
}

func normSev(raw string) string {
	l := strings.ToLower(raw)
	if v, ok := lintSevNormal[l]; ok {
		return v
	}
	return l
}

// terseDispatch is the ordered list of single-line finding patterns.
// Order matters — the first match wins, exactly as the Python list.
type terseEntry struct {
	re   *regexp.Regexp
	kind string
}

var terseDispatch = []terseEntry{
	// shellcheck: file:line:col: level: msg [SCxxxx]
	{regexp.MustCompile(`^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+(?P<sev>warning|error|info|style|note):\s+(?P<msg>.+?)\s+\[(?P<rule>SC\d+)\]\s*$`), "shellcheck"},
	// rubocop: file:line:col: C: [Correctable] Rule/Name: msg
	{regexp.MustCompile(`^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+(?P<sev>[CWERF]):\s+(?:\[Correctable\]\s+)?(?P<rule>[A-Z][\w/]*):\s+(?P<msg>.+?)\s*$`), "rubocop"},
	// stylelint: file:line:col: level  msg [rule]   (double space)
	{regexp.MustCompile(`^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+(?P<sev>warning|error)\s{2,}(?P<msg>.+?)\s+\[(?P<rule>[\w-]+)\]\s*$`), "stylelint"},
	// eslint compact: file: line N, col N, sev - msg (rule)
	{regexp.MustCompile(`^(?P<file>[^:\s][^:\n]*?):\s+line\s+(?P<line>\d+),\s+col\s+(?P<col>\d+),\s+(?P<sev>warning|error|Warning|Error)\s+-\s+(?P<msg>.+?)\s+\((?P<rule>[\w/@.-]+)\)\s*$`), "eslint-compact"},
	// eslint unix: file:line:col: msg [Sev/rule]
	{regexp.MustCompile(`^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+(?P<msg>.+?)\s+\[(?P<sev>Error|Warning)/(?P<rule>[\w/@.-]+)\]\s*$`), "eslint-unix"},
	// golangci-lint: file.go:line:col: msg (linter)
	{regexp.MustCompile(`^(?P<file>[^:\s][^:\n]*?\.go):(?P<line>\d+):(?P<col>\d+):\s+(?P<msg>.+?)\s+\((?P<linter>[\w./-]+)\)\s*$`), "golangci"},
	// gosec: [file:line] - Gxxx (CWE-nn): msg (Confidence: X, Severity: Y)
	{regexp.MustCompile(`^\[(?P<file>[^:\s\]][^:\n\]]*?):(?P<line>\d+)\]\s+-\s+(?P<rule>G\d+)\s+\((?P<cwe>CWE-\d+)\):\s+(?P<msg>.+?)\s+\(Confidence:\s+\w+,\s+Severity:\s+(?P<sev>HIGH|MEDIUM|LOW)\)\s*$`), "gosec"},
	// flake8 / ruff concise: file:line:col: CODE msg
	{regexp.MustCompile(`^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+(?P<rule>[A-Z]{1,3}\d{2,4})\s+(?P<msg>.+?)\s*$`), "flake"},
	// clippy short / gcc-style: file:line:col: sev: msg [-Wflag]?
	{regexp.MustCompile(`^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+):\s+(?P<sev>warning|error|note|help|fatal\s+error):\s+(?P<msg>.+?)(?:\s+\[(?P<rule>-[WD][^\]]+|clippy::[\w:]+)\])?\s*$`), "gcc-short"},
	// mypy: file:line: sev: msg [code]    (no col)
	{regexp.MustCompile(`^(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):\s+(?P<sev>error|warning|note):\s+(?P<msg>.+?)(?:\s+\[(?P<rule>[\w-]+)\])?\s*$`), "mypy"},
	// tsc: file(line,col): sev TSxxxx: msg
	{regexp.MustCompile(`^(?P<file>[^()\s][^()\n]*?)\((?P<line>\d+),(?P<col>\d+)\):\s+(?P<sev>error|warning)\s+(?P<rule>TS\d+):\s+(?P<msg>.+?)\s*$`), "tsc"},
}

// namedGroups returns a map of named captures for a match; absent/empty
// groups map to "".
func namedGroups(re *regexp.Regexp, m []string) map[string]string {
	gd := map[string]string{}
	for i, name := range re.SubexpNames() {
		if name == "" || i >= len(m) {
			continue
		}
		gd[name] = m[i]
	}
	return gd
}

func terseParse(line string) *finding {
	for _, e := range terseDispatch {
		m := e.re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		gd := namedGroups(e.re, m)
		sev := gd["sev"]
		if sev == "" {
			if e.kind == "golangci" {
				sev = "error"
			} else {
				sev = "info"
			}
		}
		sev = strings.ToLower(sev)
		rule := gd["rule"]
		if rule == "" {
			rule = gd["linter"]
		}
		if rule == "" && e.kind == "gcc-short" {
			if sev == "note" || sev == "help" {
				return nil
			}
			rule = "-W" + sev
		}
		if rule == "" {
			return nil
		}
		col := 0
		if gd["col"] != "" {
			col, _ = strconv.Atoi(gd["col"])
		}
		ln, _ := strconv.Atoi(gd["line"])
		return &finding{
			kind: e.kind,
			sev:  normSev(sev),
			file: strings.TrimSpace(gd["file"]),
			line: ln,
			col:  col,
			rule: rule,
			msg:  strings.TrimSpace(gd["msg"]),
		}
	}
	return nil
}

// ---- verbose-shape detection + parser ----

var (
	rustcOpener  = regexp.MustCompile(`^\s*(?P<sev>warning|error|note|help)(?:\[(?P<code>[\w:]+)\])?:\s+(?P<msg>.+?)\s*$`)
	rustcLoc     = regexp.MustCompile(`^\s*-->\s+(?P<file>[^:\s][^:\n]*?):(?P<line>\d+):(?P<col>\d+)\s*$`)
	rustcHelp    = regexp.MustCompile(`^\s*=?\s*help:\s+(?P<msg>.+?)\s*$`)
	rustcNote    = regexp.MustCompile(`^\s*=\s+note:\s+` + "`" + `#\[\w+\((?P<rule>[\w:]+)\)\]` + "`")
	rustcRuleTl  = regexp.MustCompile(`\[(?P<rule>-[WD][^\]]+|clippy::[\w:]+)\]\s*$`)
	ruffOpener   = regexp.MustCompile(`^\s*(?P<rule>[A-Z]{1,3}\d{2,4})(?:\s+\[\*\])?\s+(?P<msg>.+?)\s*$`)
	gccSourceRE  = regexp.MustCompile(`^\s*\d+\s*\|\s`)
	gccCaretRE   = regexp.MustCompile(`^\s*\|[\s~^]+$|^\s+\^[\s~^]*$`)
	eslintFileRE = regexp.MustCompile(`^(?P<file>[A-Za-z]:\\|/|\\\\|\.[\\/])\S.*$`)
	eslintRowRE  = regexp.MustCompile(`^\s+(?P<line>\d+):(?P<col>\d+)\s+(?P<sev>error|warning)\s+(?P<msg>.+?)\s+(?P<rule>[\w/@.-]+)\s*$`)
	verboseHint  = regexp.MustCompile(`(?m)^\s*(?:warning|error|note|help)(?:\[[\w:]+\])?:\s+\S|^\s*-->\s+\S+:\d+:\d+\s*$|^\s*\d+\s*\|\s|^\s+(?:\d+):(?:\d+)\s+(?:error|warning)\s|^\s*[A-Z]{1,3}\d{2,4}(?:\s+\[\*\])?\s+\S`)
)

func isVerbose(raw string) bool { return verboseHint.MatchString(raw) }

func grpNamed(re *regexp.Regexp, s string) map[string]string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	return namedGroups(re, m)
}

// isContextLine mirrors the Python "context line" test in the verbose walk.
func isContextLine(nxt string) bool {
	ls := strings.TrimLeft(nxt, " \t")
	return gccSourceRE.MatchString(nxt) || gccCaretRE.MatchString(nxt) ||
		strings.HasPrefix(ls, "|") || strings.HasPrefix(ls, "=") || strings.HasPrefix(ls, "...")
}

func parseVerbose(raw string) []*finding {
	lines := splitLinesNoTrailing(raw)
	var findings []*finding
	n := len(lines)
	eslintFile := ""
	i := 0
	for i < n {
		line := lines[i]
		stripped := strings.TrimRight(line, " \t\r\n\v\f")

		// 1. rustc/clippy/ruff multi-line block.
		if gd := grpNamed(rustcOpener, stripped); gd != nil && i+1 < n && rustcLoc.MatchString(lines[i+1]) {
			sev := strings.ToLower(gd["sev"])
			msg := strings.TrimSpace(gd["msg"])
			code := gd["code"]
			rule := ""
			if rt := grpNamed(rustcRuleTl, msg); rt != nil {
				rule = rt["rule"]
				loc := rustcRuleTl.FindStringIndex(msg)
				msg = strings.TrimRight(msg[:loc[0]], " \t")
			} else if code != "" {
				rule = code
			}
			loc := grpNamed(rustcLoc, lines[i+1])
			ln, _ := strconv.Atoi(loc["line"])
			col, _ := strconv.Atoi(loc["col"])

			j := i + 2
			help := ""
			for j < n {
				nxt := lines[j]
				if strings.TrimSpace(nxt) == "" {
					j++
					continue
				}
				if hm := grpNamed(rustcHelp, nxt); hm != nil && help == "" {
					help = strings.TrimSpace(hm["msg"])
					j++
					continue
				}
				if nm := grpNamed(rustcNote, nxt); nm != nil && rule == "" {
					rule = nm["rule"]
					j++
					continue
				}
				if isContextLine(nxt) {
					j++
					continue
				}
				if grpNamed(rustcOpener, nxt) != nil && j+1 < n && rustcLoc.MatchString(lines[j+1]) {
					break
				}
				if terseParse(strings.TrimRight(nxt, " \t\r\n\v\f")) != nil {
					break
				}
				j++
			}
			if rule == "" {
				rule = "-W" + sev
			}
			findings = append(findings, &finding{
				kind: "verbose-rustc", sev: normSev(sev), file: loc["file"],
				line: ln, col: col, rule: rule, msg: msg, help: help,
			})
			i = j
			continue
		}

		// 1b. ruff verbose.
		if rm := grpNamed(ruffOpener, stripped); rm != nil && i+1 < n && rustcLoc.MatchString(lines[i+1]) {
			rule := rm["rule"]
			msg := strings.TrimSpace(rm["msg"])
			loc := grpNamed(rustcLoc, lines[i+1])
			ln, _ := strconv.Atoi(loc["line"])
			col, _ := strconv.Atoi(loc["col"])
			j := i + 2
			help := ""
			for j < n {
				nxt := lines[j]
				if strings.TrimSpace(nxt) == "" {
					j++
					continue
				}
				if hm := grpNamed(rustcHelp, nxt); hm != nil && help == "" {
					help = strings.TrimSpace(hm["msg"])
					j++
					continue
				}
				if isContextLine(nxt) {
					j++
					continue
				}
				if grpNamed(ruffOpener, nxt) != nil && j+1 < n && rustcLoc.MatchString(lines[j+1]) {
					break
				}
				if grpNamed(rustcOpener, nxt) != nil && j+1 < n && rustcLoc.MatchString(lines[j+1]) {
					break
				}
				if terseParse(strings.TrimRight(nxt, " \t\r\n\v\f")) != nil {
					break
				}
				j++
			}
			findings = append(findings, &finding{
				kind: "verbose-ruff", sev: "warning", file: loc["file"],
				line: ln, col: col, rule: rule, msg: msg, help: help,
			})
			i = j
			continue
		}

		// 2. gcc verbose: terse opener + source lines.
		if f := terseParse(stripped); f != nil {
			j := i + 1
			help := ""
			for j < n {
				nxt := lines[j]
				if gccSourceRE.MatchString(nxt) || gccCaretRE.MatchString(nxt) || strings.TrimSpace(nxt) == "" {
					j++
					continue
				}
				if hm := grpNamed(rustcHelp, nxt); hm != nil && help == "" {
					help = strings.TrimSpace(hm["msg"])
					j++
					continue
				}
				break
			}
			f.help = help
			findings = append(findings, f)
			i = j
			continue
		}

		// 3. eslint stylish.
		if eslintFileRE.MatchString(stripped) && !strings.HasSuffix(stripped, ":") {
			eslintFile = stripped
			i++
			continue
		}
		if em := grpNamed(eslintRowRE, stripped); em != nil && eslintFile != "" {
			ln, _ := strconv.Atoi(em["line"])
			col, _ := strconv.Atoi(em["col"])
			findings = append(findings, &finding{
				kind: "eslint-stylish", sev: normSev(em["sev"]), file: eslintFile,
				line: ln, col: col, rule: em["rule"], msg: strings.TrimSpace(em["msg"]),
			})
			i++
			continue
		}

		i++
	}
	return findings
}

// (lintMaxPerRule is read per-invocation inside lintFilter — see below.)

// splitLinesNoTrailing mirrors Python str.splitlines(): split on \n
// after normalizing \r\n, dropping a single trailing empty element.
func splitLinesNoTrailing(raw string) []string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if k := len(lines); k > 0 && lines[k-1] == "" {
		lines = lines[:k-1]
	}
	return lines
}

func emitTSV(findings []*finding, maxPerRule int) string {
	if len(findings) == 0 {
		return ""
	}
	// Group by rule; track each rule's max severity + first-seen order.
	groups := map[string][]*finding{}
	ruleSev := map[string]string{}
	var order []string
	for _, f := range findings {
		if _, ok := groups[f.rule]; !ok {
			order = append(order, f.rule)
		}
		groups[f.rule] = append(groups[f.rule], f)
		if lintSevRank[f.sev] > lintSevRank[ruleSev[f.rule]] {
			ruleSev[f.rule] = f.sev
		}
	}
	// Sort: severity rank desc, then group size desc, then rule name asc.
	// Python's sorted() is stable, but the key is fully specified here.
	sort.SliceStable(order, func(a, b int) bool {
		ra, rb := order[a], order[b]
		sa, sb := lintSevRank[ruleSev[ra]], lintSevRank[ruleSev[rb]]
		if sa != sb {
			return sa > sb
		}
		la, lb := len(groups[ra]), len(groups[rb])
		if la != lb {
			return la > lb
		}
		return ra < rb
	})

	var out []string
	for _, rule := range order {
		items := groups[rule]
		kept := items
		if maxPerRule != 0 && len(items) > maxPerRule {
			kept = items[:maxPerRule]
		}
		for _, f := range kept {
			loc := f.file + ":" + strconv.Itoa(f.line)
			if f.col != 0 {
				loc += ":" + strconv.Itoa(f.col)
			}
			sevLetter, ok := lintSevLetter[f.sev]
			if !ok {
				if f.sev != "" {
					sevLetter = strings.ToUpper(f.sev[:1])
				} else {
					sevLetter = "?"
				}
			}
			out = append(out, sevLetter+"\t"+loc+"\t"+rule+"\t"+f.msg)
			if f.help != "" {
				out = append(out, "-\t"+loc+"\t"+rule+"\thelp: "+f.help)
			}
		}
		elided := len(items) - len(kept)
		if elided > 0 {
			out = append(out, "-\t\t"+rule+"\t+"+strconv.Itoa(elided)+" more "+rule)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

func lintFilter(raw []byte) []byte {
	cleaned := lintANSIRE.ReplaceAllString(string(raw), "")

	var findings []*finding
	if isVerbose(cleaned) {
		findings = parseVerbose(cleaned)
	} else {
		for _, line := range splitLinesNoTrailing(cleaned) {
			if f := terseParse(strings.TrimRight(line, " \t\r\n\v\f")); f != nil {
				findings = append(findings, f)
			}
		}
	}

	if len(findings) == 0 {
		return raw
	}

	// Read LINT_MAX_PER_RULE per-invocation: native filters run
	// in-process in a long-lived thlibo, so reading once at init would
	// freeze the knob until restart. The Python reads it per-process.
	distilled := emitTSV(findings, maxPerRuleFromEnv())

	// Monotonic: return raw verbatim if distillation didn't shrink bytes
	// (matches the Python, whose stdout is `raw` in that case). RunNative
	// also guards this, but we mirror Python so the parity test matches.
	if len(distilled) >= len(raw) {
		return raw
	}
	return []byte(distilled)
}
