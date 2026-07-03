package processors

import (
	"regexp"
	"strings"
)

func init() { RegisterNative("cargo-filter", cargoFilter) }

var (
	cargoCompilingRE  = regexp.MustCompile(`^\s*(Compiling|Checking|Downloaded|Downloading|Updating|Generating)\s`)
	cargoFinishedRE   = regexp.MustCompile(`^\s*Finished\s`)
	cargoRunningRE    = regexp.MustCompile(`^\s*Running\s`)
	cargoErrorRE      = regexp.MustCompile(`^error(\[E\d+\])?:`)
	cargoWarningRE    = regexp.MustCompile(`^warning:`)
	cargoPointerRE    = regexp.MustCompile(`^\s*-->`)
	cargoTestHeaderRE = regexp.MustCompile(`^running \d+ tests?$`)
	cargoTestResultRE = regexp.MustCompile(`^test result:`)
	cargoTestFailedRE = regexp.MustCompile(`\btest .* FAILED\b|\bFAILED$`)
)

func cargoFilter(raw []byte) []byte {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	var out []string
	keepNextIfPointer := false
	for _, line := range lines {
		if cargoCompilingRE.MatchString(line) {
			keepNextIfPointer = false
			continue
		}
		if cargoFinishedRE.MatchString(line) || cargoRunningRE.MatchString(line) {
			out = append(out, strings.TrimRight(line, "\r\n\t "))
			keepNextIfPointer = false
			continue
		}
		if cargoErrorRE.MatchString(line) || cargoWarningRE.MatchString(line) {
			out = append(out, strings.TrimRight(line, "\r\n\t "))
			keepNextIfPointer = true
			continue
		}
		if keepNextIfPointer && cargoPointerRE.MatchString(line) {
			out = append(out, strings.TrimRight(line, "\r\n\t "))
			keepNextIfPointer = false
			continue
		}
		keepNextIfPointer = false
		if cargoTestHeaderRE.MatchString(strings.TrimSpace(line)) || cargoTestResultRE.MatchString(strings.TrimSpace(line)) {
			out = append(out, strings.TrimRight(line, "\r\n\t "))
			continue
		}
		if cargoTestFailedRE.MatchString(line) {
			out = append(out, strings.TrimRight(line, "\r\n\t "))
			continue
		}
		if strings.TrimSpace(line) == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return nil
	}
	return []byte(strings.Join(out, "\n") + "\n")
}