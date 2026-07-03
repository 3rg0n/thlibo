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
	trivyAnsiRE        = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	trivyRowRE         = regexp.MustCompile(`^\s*│(.*)│\s*$`)
	trivySepRE         = regexp.MustCompile(`[─┬┼┴]`)
	trivyFullSepRE     = regexp.MustCompile(`^\s*[├┌└][─┬┼┴┤┐┘]+[┤┐┘]\s*$`)
	trivyHeaderRE      = regexp.MustCompile(`Library.*Vulnerability.*Severity.*(?:Status.*)?Installed Version.*Fixed Version.*Title`)
	trivyTargetRE      = regexp.MustCompile(`^(?P<target>\S.+?)\s+\(\S+\)\s*$`)
	trivyTotalRE       = regexp.MustCompile(`^Total:\s+\d+\s+\(`)
	trivyVulnIDRE      = regexp.MustCompile(`^(?:CVE-\d{4}-\d+|GHSA-[\w-]+|CGA-[\w-]+|[A-Z]+-\d+(?:-\d+)?)$`)
	trivyURLRE         = regexp.MustCompile(`\bhttps?://\S+`)
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
	if m == nil || len(m) < 2 {
		return []string{}
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

func trivyParseTable(lines []string, start int) ([]trivyFinding, int) {
	n := len(lines)
	i := start

	// Walk forward to find the header row to learn the column count.
	var headerCells []string
	for i < n {
		if trivyIsTableRow(lines[i]) {
			cells := trivySplitCells(lines[i])
			joined := strings.Join(cells, " | ")
			if trivyHeaderRE.MatchString(joined) {
				for _, c := range cells {
					headerCells = append(headerCells, strings.ToLower(c))
				}
				i++
				break
			}
		}
		if trivyIsSeparator(lines[i]) || trivyIsTableRow(lines[i]) {
			i++
			continue
		}
		// Not a table line at all — bail.
		return []trivyFinding{}, start + 1
	}
	if len(headerCells) == 0 {
		return []trivyFinding{}, i
	}

	// Map column index → semantic name.
	colIdx := make(map[string]int)
	for idx, name := range headerCells {
		if strings.Contains(name, "library") {
			colIdx["lib"] = idx
		} else if strings.Contains(name, "vulnerability") {
			colIdx["vuln"] = idx
		} else if strings.Contains(name, "severity") {
			colIdx["sev"] = idx
		} else if strings.Contains(name, "status") {
			colIdx["status"] = idx
		} else if strings.Contains(name, "installed") {
			colIdx["installed"] = idx
		} else if strings.Contains(name, "fixed") {
			colIdx["fixed"] = idx
		} else if strings.Contains(name, "title") {
			colIdx["title"] = idx
		}
	}

	// We need at least lib/vuln/sev/title to do useful work.
	required := []string{"lib", "vuln", "sev", "title"}
	for _, k := range required {
		if _, ok := colIdx[k]; !ok {
			return []trivyFinding{}, i
		}
	}

	var findings []trivyFinding
	var cur *trivyFinding

	flush := func() {
		if cur != nil && cur.vuln != "" {
			findings = append(findings, *cur)
		}
		cur = nil
	}

	for i < n {
		line := lines[i]
		if trivyIsSeparator(line) {
			// A separator with `┘` (right-bottom corner) ends the table.
			// `┴` alone is ambiguous: it can appear in a partial inner
			// separator too. Use trailing `┘` as the close marker.
			strippedEnd := strings.TrimRight(line, " ")
			if strings.HasSuffix(strippedEnd, "┘") {
				flush()
				return findings, i + 1
			}
			// Separator BETWEEN rows. If the vuln-column of the
			// following row is blank, it's a wrap continuation;
			// otherwise it's a new finding.
			if i+1 < n && trivyIsTableRow(lines[i+1]) {
				nextCells := trivySplitCells(lines[i+1])
				if len(nextCells) > colIdx["vuln"] && nextCells[colIdx["vuln"]] != "" {
					flush()
				}
			}
			i++
			continue
		}

		if trivyIsTableRow(line) {
			cells := trivySplitCells(line)
			maxIdx := 0
			for _, v := range colIdx {
				if v > maxIdx {
					maxIdx = v
				}
			}
			if len(cells) <= maxIdx {
				i++
				continue
			}
			if cur == nil {
				cur = &trivyFinding{
					lib:       cells[colIdx["lib"]],
					vuln:      cells[colIdx["vuln"]],
					sev:       cells[colIdx["sev"]],
					status:    trivyGetCell(cells, colIdx["status"]),
					installed: trivyGetCell(cells, colIdx["installed"]),
					fixed:     trivyGetCell(cells, colIdx["fixed"]),
					title:     cells[colIdx["title"]],
				}
			} else {
				// Continuation: empty cells inherit the prior value;
				// non-empty cells append to the title (the most common
				// wrap target) or replace the column value.
				for key := range colIdx {
					idx := colIdx[key]
					if idx >= len(cells) {
						continue
					}
					val := cells[idx]
					if val == "" {
						continue
					}
					if key == "title" {
						// Append wrapped title text with a single space.
						cur.title = strings.TrimSpace(cur.title + " " + val)
					} else {
						// Other columns: a non-empty cell on a
						// continuation row means a NEW value (e.g.
						// severity changed). Replace.
						switch key {
						case "lib":
							if cur.lib == "" {
								cur.lib = val
							} else {
								// Pre-populated; start fresh.
								flush()
								cur = &trivyFinding{
									lib:       val,
									vuln:      trivyGetCell(cells, colIdx["vuln"]),
									sev:       trivyGetCell(cells, colIdx["sev"]),
									status:    trivyGetCell(cells, colIdx["status"]),
									installed: trivyGetCell(cells, colIdx["installed"]),
									fixed:     trivyGetCell(cells, colIdx["fixed"]),
									title:     trivyGetCell(cells, colIdx["title"]),
								}
								break
							}
						case "vuln":
							if cur.vuln == "" {
								cur.vuln = val
							} else {
								flush()
								cur = &trivyFinding{
									lib:       trivyGetCell(cells, colIdx["lib"]),
									vuln:      val,
									sev:       trivyGetCell(cells, colIdx["sev"]),
									status:    trivyGetCell(cells, colIdx["status"]),
									installed: trivyGetCell(cells, colIdx["installed"]),
									fixed:     trivyGetCell(cells, colIdx["fixed"]),
									title:     trivyGetCell(cells, colIdx["title"]),
								}
								break
							}
						case "sev":
							if cur.sev == "" {
								cur.sev = val
							} else {
								flush()
								cur = &trivyFinding{
									lib:       trivyGetCell(cells, colIdx["lib"]),
									vuln:      trivyGetCell(cells, colIdx["vuln"]),
									sev:       val,
									status:    trivyGetCell(cells, colIdx["status"]),
									installed: trivyGetCell(cells, colIdx["installed"]),
									fixed:     trivyGetCell(cells, colIdx["fixed"]),
									title:     trivyGetCell(cells, colIdx["title"]),
								}
								break
							}
						case "status":
							if cur.status == "" {
								cur.status = val
							} else {
								flush()
								cur = &trivyFinding{
									lib:       trivyGetCell(cells, colIdx["lib"]),
									vuln:      trivyGetCell(cells, colIdx["vuln"]),
									sev:       trivyGetCell(cells, colIdx["sev"]),
									status:    val,
									installed: trivyGetCell(cells, colIdx["installed"]),
									fixed:     trivyGetCell(cells, colIdx["fixed"]),
									title:     trivyGetCell(cells, colIdx["title"]),
								}
								break
							}
						case "installed":
							if cur.installed == "" {
								cur.installed = val
							} else {
								flush()
								cur = &trivyFinding{
									lib:       trivyGetCell(cells, colIdx["lib"]),
									vuln:      trivyGetCell(cells, colIdx["vuln"]),
									sev:       trivyGetCell(cells, colIdx["sev"]),
									status:    trivyGetCell(cells, colIdx["status"]),
									installed: val,
									fixed:     trivyGetCell(cells, colIdx["fixed"]),
									title:     trivyGetCell(cells, colIdx["title"]),
								}
								break
							}
						case "fixed":
							if cur.fixed == "" {
								cur.fixed = val
							} else {
								flush()
								cur = &trivyFinding{
									lib:       trivyGetCell(cells, colIdx["lib"]),
									vuln:      trivyGetCell(cells, colIdx["vuln"]),
									sev:       trivyGetCell(cells, colIdx["sev"]),
									status:    trivyGetCell(cells, colIdx["status"]),
									installed: trivyGetCell(cells, colIdx["installed"]),
									fixed:     val,
									title:     trivyGetCell(cells, colIdx["title"]),
								}
								break
							}
						}
					}
				}
			}
			i++
			continue
		}

		// Non-table line inside the table region — skip.
		i++
	}

	flush()
	return findings, i
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
