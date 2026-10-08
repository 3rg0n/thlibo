package processors

import (
	"regexp"
	"sort"
	"strings"
)

// trivy-filter: distill Trivy's box-drawing tables into TSV.
// Native Go port of processors/trivy-filter/run.py (ADR 0010).
// Behaviour parity is locked by tests against the Python reference.
//
// Trivy's default output is a series of unicode box-drawing tables, one
// per scanned target. Each row is enclosed in `│ ... │`, and a single
// finding can span multiple visual rows when its title or fixed-version
// list wraps. Logical rows are separated by `├──...──┤` separators.
//
// Output schema (5 columns, tab-separated):
//   severity \t lib@installed \t CVE \t fixed-version \t title
//
// severity letter: C=critical, H=high, M=medium, L=low, U=unknown.

func init() { RegisterNative("trivy-filter", trivyFilter) }

var (
	trivyAnsiRE   = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	trivyRowRE    = regexp.MustCompile(`^\s*│(.*)│\s*$`)
	trivyHeaderRE = regexp.MustCompile(`Library.*Vulnerability.*Severity.*(?:Status.*)?Installed Version.*Fixed Version.*Title`)
	trivyVulnIDRE = regexp.MustCompile(`^(?:CVE-\d{4}-\d+|GHSA-[\w-]+|CGA-[\w-]+|[A-Z]+-\d+(?:-\d+)?)$`)
	trivyURLRE    = regexp.MustCompile(`\bhttps?://\S+`)
)

var (
	trivySevLetter = map[string]string{
		"critical": "C",
		"high":     "H",
		"medium":   "M",
		"low":      "L",
		"unknown":  "U",
	}
	trivySevRank = map[string]int{
		"critical": 5,
		"high":     4,
		"medium":   3,
		"low":      2,
		"unknown":  1,
	}
)

type trivyFinding struct {
	lib       string
	vuln      string
	sev       string
	status    string
	installed string
	fixed     string
	title     string
}

// trivyTableParser holds state for parsing a single Trivy table.
type trivyTableParser struct {
	lines  []string
	start  int
	n      int
	colIdx map[string]int
	// colOrder is colIdx's keys in first-insertion order. The continuation
	// merge must visit columns in that order: run.py iterates its
	// insertion-ordered col_idx dict, and Go randomises map range order.
	colOrder []string
	i        int
	cur      *trivyFinding
	findings []trivyFinding
}

func trivyFilter(raw []byte) []byte {
	cleaned := trivyAnsiRE.ReplaceAllString(string(raw), "")
	lines := strings.Split(cleaned, "\n")
	// splitlines() semantics: a trailing newline shouldn't yield a final
	// empty element. Python's str.splitlines() drops the trailing "".
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	var allFindings []trivyFinding
	i := 0
	n := len(lines)
	for i < n {
		if trivyIsTableOpen(lines[i]) {
			fs, j := trivyParseTable(lines, i)
			if j == i {
				j = i + 1
			}
			// Skip the small "Report Summary" overview table at
			// the top — it has Target/Type/Vulnerabilities columns,
			// no CVEs.
			if len(fs) > 0 {
				allFindings = append(allFindings, fs...)
			}
			i = j
			continue
		}
		i++
	}

	if len(allFindings) == 0 {
		return raw
	}

	distilled := trivyEmitTSV(allFindings)
	if strings.TrimSpace(distilled) == "" {
		return raw
	}

	if len(distilled) >= len(string(raw)) {
		return raw
	}
	return []byte(distilled)
}

func trivyIsTableOpen(line string) bool {
	s := strings.TrimSpace(line)
	return strings.HasPrefix(s, "┌") && strings.HasSuffix(s, "┐")
}

func trivySplitCells(line string) []string {
	m := trivyRowRE.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	inner := m[1]
	parts := strings.Split(inner, "│")
	var out []string
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func trivyIsTableRow(line string) bool {
	// A data row contains `│` but no horizontal-box-drawing characters.
	if !strings.Contains(line, "│") {
		return false
	}
	return !strings.Contains(line, "─")
}

func trivyIsSeparator(line string) bool {
	// Anything inside the table that contains `─` is a separator
	// (full or partial — partial separators start with `│   ├──┤` etc).
	hasHyphen := strings.Contains(line, "─")
	hasBox := strings.Contains(line, "│") || strings.Contains(line, "├") ||
		strings.Contains(line, "└") || strings.Contains(line, "┌")
	return hasHyphen && hasBox
}

// findHeader scans forward from tp.i to locate and parse the header row,
// populating tp.colIdx. Returns true if header found, false otherwise.
// Updates tp.i to point after the header.
func (tp *trivyTableParser) findHeader() bool {
	for tp.i < tp.n {
		if trivyIsTableRow(tp.lines[tp.i]) {
			cells := trivySplitCells(tp.lines[tp.i])
			joined := strings.Join(cells, " | ")
			if trivyHeaderRE.MatchString(joined) {
				tp.colIdx = make(map[string]int)
				tp.colOrder = nil
				for idx, c := range cells {
					name := strings.ToLower(c)
					if strings.Contains(name, "library") {
						tp.setCol("lib", idx)
					} else if strings.Contains(name, "vulnerability") {
						tp.setCol("vuln", idx)
					} else if strings.Contains(name, "severity") {
						tp.setCol("sev", idx)
					} else if strings.Contains(name, "status") {
						tp.setCol("status", idx)
					} else if strings.Contains(name, "installed") {
						tp.setCol("installed", idx)
					} else if strings.Contains(name, "fixed") {
						tp.setCol("fixed", idx)
					} else if strings.Contains(name, "title") {
						tp.setCol("title", idx)
					}
				}
				tp.i++
				return true
			}
		}
		if trivyIsSeparator(tp.lines[tp.i]) || trivyIsTableRow(tp.lines[tp.i]) {
			tp.i++
			continue
		}
		// Not a table line — bail
		return false
	}
	return false
}

// hasRequiredColumns checks if colIdx has all required columns.
func (tp *trivyTableParser) hasRequiredColumns() bool {
	required := []string{"lib", "vuln", "sev", "title"}
	for _, k := range required {
		if _, ok := tp.colIdx[k]; !ok {
			return false
		}
	}
	return true
}

// setCol maps a semantic column name to its index, recording first-insertion
// order in colOrder.
func (tp *trivyTableParser) setCol(key string, idx int) {
	if _, ok := tp.colIdx[key]; !ok {
		tp.colOrder = append(tp.colOrder, key)
	}
	tp.colIdx[key] = idx
}

// flush appends the current finding if it's valid.
func (tp *trivyTableParser) flush() {
	if tp.cur != nil && tp.cur.vuln != "" {
		tp.findings = append(tp.findings, *tp.cur)
	}
	tp.cur = nil
}

// updateCurrent merges cells into the current finding. If a key column
// (lib/vuln/sev/status/installed/fixed) is non-empty on a continuation,
// and it's already populated, flushes the current finding and starts a new one.
// Titles are always merged (wrapped lines).
func (tp *trivyTableParser) updateCurrent(cells []string) {
	if tp.cur == nil {
		// First row of a finding.
		tp.cur = &trivyFinding{
			lib:       trivyGetCell(cells, tp.colIdx["lib"]),
			vuln:      trivyGetCell(cells, tp.colIdx["vuln"]),
			sev:       trivyGetCell(cells, tp.colIdx["sev"]),
			status:    trivyGetCell(cells, tp.colIdx["status"]),
			installed: trivyGetCell(cells, tp.colIdx["installed"]),
			fixed:     trivyGetCell(cells, tp.colIdx["fixed"]),
			title:     trivyGetCell(cells, tp.colIdx["title"]),
		}
		return
	}

	// Continuation row: merge or replace fields.
	for _, key := range tp.colOrder {
		idx := tp.colIdx[key]
		if idx >= len(cells) {
			continue
		}
		val := cells[idx]
		if val == "" {
			continue
		}
		if key == "title" {
			// Title wraps: append with a space.
			tp.cur.title = strings.TrimSpace(tp.cur.title + " " + val)
		} else {
			// Other fields: replace if empty, or flush+start new if already set.
			updated := false
			switch key {
			case "lib":
				if tp.cur.lib == "" {
					tp.cur.lib = val
					updated = true
				}
			case "vuln":
				if tp.cur.vuln == "" {
					tp.cur.vuln = val
					updated = true
				}
			case "sev":
				if tp.cur.sev == "" {
					tp.cur.sev = val
					updated = true
				}
			case "status":
				if tp.cur.status == "" {
					tp.cur.status = val
					updated = true
				}
			case "installed":
				if tp.cur.installed == "" {
					tp.cur.installed = val
					updated = true
				}
			case "fixed":
				if tp.cur.fixed == "" {
					tp.cur.fixed = val
					updated = true
				}
			}
			// If field was already set, start a new finding.
			if !updated {
				tp.flush()
				tp.cur = &trivyFinding{
					lib:       trivyGetCell(cells, tp.colIdx["lib"]),
					vuln:      trivyGetCell(cells, tp.colIdx["vuln"]),
					sev:       trivyGetCell(cells, tp.colIdx["sev"]),
					status:    trivyGetCell(cells, tp.colIdx["status"]),
					installed: trivyGetCell(cells, tp.colIdx["installed"]),
					fixed:     trivyGetCell(cells, tp.colIdx["fixed"]),
					title:     trivyGetCell(cells, tp.colIdx["title"]),
				}
				// Starting a fresh finding ends this row's merge, as
				// run.py's `break` does; carrying on would append the
				// row's title to the new finding a second time.
				return
			}
		}
	}
}

func trivyParseTable(lines []string, start int) ([]trivyFinding, int) {
	tp := &trivyTableParser{
		lines:  lines,
		start:  start,
		n:      len(lines),
		i:      start,
		colIdx: make(map[string]int),
	}

	// Find header row and populate column mappings.
	if !tp.findHeader() {
		return []trivyFinding{}, tp.start + 1
	}
	if !tp.hasRequiredColumns() {
		return []trivyFinding{}, tp.i
	}

	// Parse table rows and separators.
	for tp.i < tp.n {
		line := tp.lines[tp.i]

		if trivyIsSeparator(line) {
			// Check for table-end marker.
			strippedEnd := strings.TrimRight(line, " ")
			if strings.HasSuffix(strippedEnd, "┘") {
				tp.flush()
				return tp.findings, tp.i + 1
			}
			// Between-row separator: check if next row starts a new finding.
			if tp.i+1 < tp.n && trivyIsTableRow(tp.lines[tp.i+1]) {
				nextCells := trivySplitCells(tp.lines[tp.i+1])
				if len(nextCells) > tp.colIdx["vuln"] && nextCells[tp.colIdx["vuln"]] != "" {
					tp.flush()
				}
			}
			tp.i++
			continue
		}

		if trivyIsTableRow(line) {
			cells := trivySplitCells(line)
			// Ensure we have enough cells to read all columns.
			maxIdx := 0
			for _, v := range tp.colIdx {
				if v > maxIdx {
					maxIdx = v
				}
			}
			if len(cells) > maxIdx {
				tp.updateCurrent(cells)
			}
			tp.i++
			continue
		}

		// Non-table line — skip.
		tp.i++
	}

	tp.flush()
	return tp.findings, tp.i
}

func trivyGetCell(cells []string, idx int) string {
	if idx < 0 || idx >= len(cells) {
		return ""
	}
	return cells[idx]
}

func trivyEmitTSV(findings []trivyFinding) string {
	if len(findings) == 0 {
		return ""
	}

	// Drop URL noise from titles, propagate inherited fields, then
	// sort by severity (criticals first), library name.
	type tsv struct {
		sevRank int
		line    string
	}
	var rows []tsv
	lastLib := ""
	lastSev := ""
	lastInstalled := ""

	for _, f := range findings {
		// Continuations may carry forward the lib/sev/installed cells
		// as blanks — promote.
		lib := f.lib
		if lib == "" {
			lib = lastLib
		}
		sevRaw := strings.ToLower(strings.TrimSpace(f.sev))
		if sevRaw == "" {
			sevRaw = lastSev
		}
		installed := f.installed
		if installed == "" {
			installed = lastInstalled
		}

		if lib != "" {
			lastLib = lib
		}
		if sevRaw != "" {
			lastSev = sevRaw
		}
		if installed != "" {
			lastInstalled = installed
		}

		if f.vuln == "" || !trivyVulnIDRE.MatchString(f.vuln) {
			continue
		}

		title := trivyURLRE.ReplaceAllString(f.title, "")
		title = strings.TrimSpace(title)
		title = regexp.MustCompile(`\s+`).ReplaceAllString(title, " ")

		sevLetter := trivySevLetter[sevRaw]
		if sevLetter == "" {
			if sevRaw != "" {
				sevLetter = strings.ToUpper(sevRaw[:1])
			} else {
				sevLetter = "?"
			}
		}

		libAt := lib
		if installed != "" {
			libAt = lib + "@" + installed
		}

		fixed := strings.TrimSpace(f.fixed)
		if fixed == "" {
			fixed = "-"
		}

		line := sevLetter + "\t" + libAt + "\t" + f.vuln + "\t" + fixed + "\t" + title
		sevRank := trivySevRank[sevRaw]
		rows = append(rows, tsv{sevRank: sevRank, line: line})
	}

	// Sort by severity descending, then by line text.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].sevRank != rows[j].sevRank {
			return rows[i].sevRank > rows[j].sevRank
		}
		return rows[i].line < rows[j].line
	})

	var out []string
	for _, row := range rows {
		out = append(out, row.line)
	}

	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}
