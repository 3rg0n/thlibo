package processors

import (
	"regexp"
	"strings"
)

func init() { RegisterNative("npm-filter", npmFilter) }

var (
	npmTreeGlyphRE = regexp.MustCompile(`^\s*[+\-`+"`"+`]+\s*(?:UNMET DEPENDENCY\s+)?(.+?)\s*$`)
	npmHeaderRE    = regexp.MustCompile(`^npm (error|ERR!|warn|WARN|notice)`)
	npmAuditSevRE  = regexp.MustCompile(`(?i)^\s*(low|moderate|high|critical)\s+severity`)
	npmCountsRE    = regexp.MustCompile(`^(added|removed|changed|audited|up to date)\s+\d`)
	npmPkgLineRE   = regexp.MustCompile(`^[A-Za-z0-9_@\-/.]+@[\d.\w\-+]+$`)
)

func npmFilter(raw []byte) []byte {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	var out []string
	seenNotice := map[string]bool{}
	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		if m := npmHeaderRE.FindStringSubmatch(stripped); m != nil {
			if strings.EqualFold(m[1], "notice") {
				if seenNotice[stripped] {
					continue
				}
				seenNotice[stripped] = true
			}
			out = append(out, stripped)
			continue
		}
		if npmCountsRE.MatchString(stripped) {
			out = append(out, stripped)
			continue
		}
		if npmAuditSevRE.MatchString(stripped) {
			out = append(out, stripped)
			continue
		}
		if npmPkgLineRE.MatchString(stripped) {
			out = append(out, stripped)
			continue
		}
		if m := npmTreeGlyphRE.FindStringSubmatch(line); m != nil {
			name := strings.TrimSpace(m[1])
			if npmPkgLineRE.MatchString(name) {
				out = append(out, name)
				continue
			}
			continue
		}
		if strings.HasPrefix(stripped, "#") {
			out = append(out, stripped)
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