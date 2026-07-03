package processors

import (
	"encoding/json"
	"regexp"
	"strings"
)

func init() { RegisterNative("go-test-filter", goTestFilter) }

var (
	goTestANSIRE           = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	goTestRunRE            = regexp.MustCompile(`^\s*=== (?:RUN|PAUSE|CONT|NAME)\s`)
	goTestPassRE           = regexp.MustCompile(`^\s*--- PASS:\s`)
	goTestFailRE           = regexp.MustCompile(`^\s*--- FAIL:\s+(?P<name>\S+)`)
	goTestSkipRE           = regexp.MustCompile(`^\s*--- SKIP:\s`)
	goTestPkgOkRE          = regexp.MustCompile(`^ok\s+\S+`)
	goTestPkgFailRE        = regexp.MustCompile(`^FAIL\s+\S+`)
	goTestPkgNotestRE      = regexp.MustCompile(`^\?\s+\S+\s+\[no test files\]`)
	goTestBareResultRE     = regexp.MustCompile(`^(?:PASS|FAIL)\s*$`)
	goTestBuildHdrRE       = regexp.MustCompile(`^# \S`)
	goTestBuildFailRE      = regexp.MustCompile(`^FAIL\s+\S+\s+\[build failed\]`)
	goTestPanicRE          = regexp.MustCompile(`^(panic:|fatal error:)\s`)
	goTestBlockBoundaryRE  = regexp.MustCompile(`^\s*(?:=== |--- (?:PASS|FAIL|SKIP):)`)
)

type goTestEvent struct {
	Time    string `json:"Time"`
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

func compressJSON(lines []string) []string {
	out := []string{}
	failedOutput := make(map[string][]string)
	var failOrder []string
	var pkgResults []string
	skipped := 0
	sawJSON := false

	for _, ln := range lines {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		var ev goTestEvent
		if err := json.Unmarshal([]byte(s), &ev); err != nil {
			return nil
		}
		if ev.Action == "" {
			return nil
		}
		sawJSON = true
		switch ev.Action {
		case "output":
			if ev.Test != "" {
				failedOutput[ev.Test] = append(failedOutput[ev.Test], strings.TrimSuffix(ev.Output, "\n"))
			}
		case "fail":
			if ev.Test != "" && (len(failOrder) == 0 || !goTestContains(failOrder, ev.Test)) {
				failOrder = append(failOrder, ev.Test)
			} else if ev.Package != "" {
				pkgResults = append(pkgResults, "FAIL "+ev.Package)
			}
		case "pass":
			if ev.Package != "" && ev.Test == "" {
				pkgResults = append(pkgResults, "ok "+ev.Package)
			}
		case "skip":
			if ev.Test != "" {
				skipped++
			}
		}
	}

	if !sawJSON {
		return nil
	}

	for _, name := range failOrder {
		out = append(out, "--- FAIL: "+name)
		for _, o := range failedOutput[name] {
			t := strings.TrimSpace(o)
			if t != "" && !goTestBlockBoundaryRE.MatchString(o) {
				out = append(out, "    "+t)
			}
		}
	}

	if skipped > 0 {
		out = append(out, "(skipped "+goTestItoa(skipped)+" test(s))")
	}
	out = append(out, pkgResults...)
	return out
}

func goTestContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func compressText(lines []string) []string {
	out := []string{}
	buf := []string{}
	inTest := false
	skipped := 0

	for i := 0; i < len(lines); i++ {
		raw := goTestANSIRE.ReplaceAllString(lines[i], "")
		raw = strings.TrimRight(raw, " \t\r\n")

		if goTestBuildHdrRE.MatchString(raw) {
			out = append(out, raw)
			i++
			for i < len(lines) {
				nxt := goTestANSIRE.ReplaceAllString(lines[i], "")
				nxt = strings.TrimRight(nxt, " \t\r\n")
				if nxt == "" || goTestPkgFailRE.MatchString(nxt) || goTestBuildFailRE.MatchString(nxt) {
					break
				}
				out = append(out, nxt)
				i++
			}
			i--
			continue
		}

		if goTestPanicRE.MatchString(raw) {
			out = append(out, raw)
			i++
			for i < len(lines) {
				nxt := goTestANSIRE.ReplaceAllString(lines[i], "")
				nxt = strings.TrimRight(nxt, " \t\r\n")
				if goTestPkgFailRE.MatchString(nxt) || goTestPkgOkRE.MatchString(nxt) {
					break
				}
				out = append(out, nxt)
				i++
			}
			i--
			continue
		}

		if goTestRunRE.MatchString(raw) {
			inTest = true
			buf = []string{}
			continue
		}

		if goTestFailRE.MatchString(raw) {
			out = append(out, buf...)
			out = append(out, raw)
			buf = []string{}
			inTest = false
			continue
		}
		if goTestPassRE.MatchString(raw) {
			buf = []string{}
			inTest = false
			continue
		}
		if goTestSkipRE.MatchString(raw) {
			skipped++
			buf = []string{}
			inTest = false
			continue
		}

		if goTestPkgOkRE.MatchString(raw) || goTestPkgFailRE.MatchString(raw) ||
			goTestPkgNotestRE.MatchString(raw) || goTestBuildFailRE.MatchString(raw) {
			out = append(out, buf...)
			buf = []string{}
			inTest = false
			out = append(out, raw)
			continue
		}
		if raw == "PASS" {
			buf = []string{}
			continue
		}
		if goTestBareResultRE.MatchString(raw) {
			out = append(out, raw)
			continue
		}

		if raw == "" {
			continue
		}
		if inTest {
			buf = append(buf, raw)
		} else {
			out = append(out, raw)
		}
	}

	out = append(out, buf...)

	if skipped > 0 {
		out = append(out, "(skipped "+goTestItoa(skipped)+" test(s))")
	}
	return out
}

func goTestFilter(raw []byte) []byte {
	text := string(raw)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var result []string
	compressed := compressJSON(lines)
	if compressed == nil {
		compressed = compressText(lines)
	}

	result = compressed

	for len(result) > 0 && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}

	if len(result) == 0 {
		return nil
	}
	return []byte(strings.Join(result, "\n") + "\n")
}

func goTestItoa(n int) string {
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
