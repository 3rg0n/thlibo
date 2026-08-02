package processors

// cordon-filter, part 1: per-line signature and level extraction.
//
// This half is a byte-for-byte port of the Python run.py's _signature /
// _level / _extract_keys and is covered by a golden parity test, so the
// odd-looking choices below are faithful rather than considered. Where a
// Python idiom has no direct Go equivalent the comment says which
// Python behaviour is being reproduced.
//
// Part 2 (cordon.go) is the windowing, embedding and k-NN scoring.

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	cordonTokenDigits = regexp.MustCompile(`\d+`)
	cordonTokenHex    = regexp.MustCompile(`\b[0-9a-fA-F]{8,}\b`)
	cordonTokenUUID   = regexp.MustCompile(
		`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	cordonTokenIP = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

	// Anchored variants for the per-path-segment tests, which use
	// Python's re.fullmatch.
	cordonFullUUID = regexp.MustCompile(`^(?:` + cordonTokenUUID.String() + `)$`)
	cordonFullHex  = regexp.MustCompile(`^(?:` + cordonTokenHex.String() + `)$`)

	cordonNonSigChars   = regexp.MustCompile(`[^A-Za-z0-9<>/-]+`)
	cordonNonPlainChars = regexp.MustCompile(`[^A-Za-z0-9<>]+`)
	cordonAlphaWords    = regexp.MustCompile(`[A-Za-z]+`)
)

// cordonShapeKeys are the fields lifted out of structured (JSON / Loki /
// OTel) records to build a discriminating signature.
//
// ORDER IS LOAD-BEARING, for two separate reasons. Most-discriminating
// fields come first so the 200-char cap doesn't cut them off — path stems
// and msg stems vary per record, while service identifiers are near
// constant within one stream, so those sit at the back. And because the
// signature is the joined string, reordering these silently changes every
// signature the filter has ever produced.
//
// The regression this shape exists for: a traefik access log where every
// line carries the same level and msg collapsed to ONE signature, so
// grouping by it discarded exactly the variation cordon is meant to
// surface.
var cordonShapeKeys = []string{
	"level", "detected_level", "severity", "lvl",
	"method", "RequestMethod", "http.method",
	"status", "status_code", "DownstreamStatus", "OriginStatus",
	"RequestPath", "path", "url.path",
	"caller", "logger", "log.logger",
	"msg", "message", "error", "err",
	"action", "event",
	"RouterName", "ServiceName", "service_name", "service",
}

// Key groups that get value-specific treatment before joining. Kept as
// sets so the switch in cordonSignature reads like the Python's chained
// `elif k in (...)`.
var (
	cordonStatusKeys = map[string]bool{
		"status": true, "status_code": true,
		"DownstreamStatus": true, "OriginStatus": true,
	}
	cordonPathKeys = map[string]bool{
		"RequestPath": true, "path": true, "url.path": true,
	}
	cordonMsgKeys = map[string]bool{
		"msg": true, "message": true, "error": true, "err": true,
	}
)

// cordonTryJSON parses a record as a JSON object, tolerating a leading
// log prefix (`ts="..." {...}`, the vector/Loki shape) by scanning to the
// first `{`. Returns nil for anything that isn't an object — including a
// JSON array, which is valid JSON but has no fields to lift.
//
// Trailing content after the object is tolerated on purpose: Python's
// json.loads rejects it, so the Python took the plain-text branch for
// such a line, and json.Decoder without a second Decode call reproduces
// exactly the opposite. The explicit trailing check below is what keeps
// the two the same.
func cordonTryJSON(line string) map[string]any {
	s := strings.TrimLeft(line, " \t\n\r\f\v")
	if s == "" {
		return nil
	}
	if s[0] != '{' {
		i := strings.IndexByte(s, '{')
		if i < 0 {
			return nil
		}
		s = s[i:]
	}
	var obj map[string]any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber() // so 3 stays "3" rather than becoming "3" via float64 formatting
	if err := dec.Decode(&obj); err != nil {
		return nil
	}
	// Python's json.loads is whole-string: `{"a":1} trailing` raises, so
	// that line took the plain-text branch. json.Decoder stops at the end
	// of the first value instead, so anything but a clean EOF after the
	// object — a second value, or junk that doesn't tokenise — has to be
	// rejected here or the two implementations diverge on the same input.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil
	}
	return obj
}

// cordonFlatGet returns obj[key] as a string when it is a scalar, else
// looks one level down in labels / attributes / fields (the Loki and OTel
// shapes). Returns ("", false) for a missing key and for a dict/list
// value, which carries no usable scalar.
func cordonFlatGet(obj map[string]any, key string) (string, bool) {
	v, ok := obj[key]
	if !ok || v == nil {
		for _, nest := range []string{"labels", "attributes", "fields"} {
			sub, isMap := obj[nest].(map[string]any)
			if !isMap {
				continue
			}
			if nv, present := sub[key]; present && nv != nil {
				v, ok = nv, true
				break
			}
		}
	}
	if !ok || v == nil {
		return "", false
	}
	switch v.(type) {
	case map[string]any, []any:
		return "", false
	}
	return cordonScalarString(v), true
}

// cordonScalarString renders a JSON scalar the way Python's str() does,
// which is the format the signature was built from. The two differences
// that matter: Python spells booleans True/False, and it prints an
// integral float as `1.0` rather than Go's `1`. json.Number preserves the
// document's own text, so a numeric level of 3 stays "3".
func cordonScalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case json.Number:
		return t.String()
	case float64:
		// Reached only if UseNumber is ever dropped. Keep Python's
		// repr-ish shape rather than Go's shortest form.
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10) + ".0"
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return ""
	}
}

// cordonStatusClass buckets a status code by its first digit: 200 -> 2xx,
// 503 -> 5xx. Bucketing keeps the signature stable across codes that mean
// the same thing (404 vs 410, 502 vs 504). Fewer than three digits is not
// a status code, so the raw value is kept.
func cordonStatusClass(status string) string {
	var digits strings.Builder
	for _, r := range status {
		if unicode.IsDigit(r) {
			digits.WriteRune(r)
		}
	}
	d := digits.String()
	if len(d) >= 3 {
		return d[:1] + "xx"
	}
	return ""
}

// cordonPathStem keeps up to 4 path segments, tokenising numeric / hex /
// UUID ones so /api/users/42 and /api/users/99 collapse together.
func cordonPathStem(path string) string {
	if path == "" {
		return ""
	}
	p := path
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if i := strings.IndexByte(p, '#'); i >= 0 {
		p = p[:i]
	}
	var parts []string
	for _, seg := range strings.Split(p, "/") {
		if seg != "" {
			parts = append(parts, seg)
		}
	}
	if len(parts) == 0 {
		return "/"
	}
	if len(parts) > 4 {
		parts = parts[:4]
	}
	out := make([]string, 0, len(parts))
	for _, seg := range parts {
		switch {
		case cordonFullUUID.MatchString(seg):
			out = append(out, "<uuid>")
		case cordonFullHex.MatchString(seg):
			out = append(out, "<hex>")
		case cordonAllDigits(seg):
			out = append(out, "<n>")
		default:
			out = append(out, seg)
		}
	}
	return "/" + strings.Join(out, "/")
}

// cordonAllDigits mirrors Python's str.isdigit for the ASCII case: every
// rune a digit, and not empty. (Python also accepts superscripts and
// other Unicode digit forms; a path segment of "²" is not a case any log
// produces, and treating it as text is the safer of the two answers.)
func cordonAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// cordonSignature builds the stable kebab-case signature for one line.
//
// Two paths. Structured records lift the discriminating keys above; plain
// text is token-replaced and truncated to 80 chars. The 80 (rather than
// the original 40) is what gives an unstructured record room to
// differentiate once shared boilerplate has collapsed.
func cordonSignature(line string) string {
	if obj := cordonTryJSON(line); obj != nil {
		var parts []string
		seen := map[string]bool{}
		for _, k := range cordonShapeKeys {
			v, ok := cordonFlatGet(obj, k)
			if !ok {
				continue
			}
			switch {
			case cordonStatusKeys[k]:
				if c := cordonStatusClass(v); c != "" {
					v = c
				}
			case cordonPathKeys[k]:
				if st := cordonPathStem(v); st != "" {
					v = st
				}
			case cordonMsgKeys[k]:
				// The first three alphabetic words are usually the
				// discriminating phrase ("Retry exhausted for", "invalid
				// character looking").
				words := cordonAlphaWords.FindAllString(v, 3)
				for i, w := range words {
					words[i] = strings.ToLower(w)
				}
				v = strings.Join(words, "-")
			default:
				v = strings.ToLower(v)
			}
			v = strings.Trim(cordonNonSigChars.ReplaceAllString(v, "-"), "-")
			if v == "" {
				continue
			}
			// Dotted keys report their last segment: http.method -> method.
			short := strings.ToLower(k)
			if i := strings.LastIndexByte(short, '.'); i >= 0 {
				short = short[i+1:]
			}
			tag := short + "=" + v
			if !seen[tag] {
				seen[tag] = true
				parts = append(parts, tag)
			}
		}
		if len(parts) > 0 {
			return cordonTruncate(strings.Join(parts, ";"), 200)
		}
		// No usable field: fall through to the plain-text path, exactly as
		// the Python did — a JSON record of unknown shape is still text.
	}

	s := strings.TrimSpace(line)
	s = cordonTokenUUID.ReplaceAllString(s, "<uuid>")
	s = cordonTokenIP.ReplaceAllString(s, "<ip>")
	s = cordonTokenHex.ReplaceAllString(s, "<hex>")
	s = cordonTokenDigits.ReplaceAllString(s, "<n>")
	s = strings.ToLower(strings.Trim(cordonNonPlainChars.ReplaceAllString(s, "-"), "-"))
	if s == "" {
		return "unknown"
	}
	return cordonTruncate(s, 80)
}

// cordonTruncate cuts to n *bytes*, matching Python's slice on a str only
// for ASCII. Every signature reaching here has been through a character
// filter that keeps ASCII alphanumerics plus `<>/-`, so the two agree;
// the rune guard is here so a future filter change degrades to a valid
// UTF-8 string rather than a split rune.
func cordonTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8StartByte(s[n]) {
		n--
	}
	return s[:n]
}

func utf8StartByte(b byte) bool { return b&0xC0 != 0x80 }

var cordonLevelRE = regexp.MustCompile(
	`(?i)\b(fatal|panic|error|err|critical|crit|warn|warning|info|debug|trace)\b`)

var cordonLevelNormalise = map[string]string{
	"panic":    "error",
	"err":      "error",
	"critical": "error",
	"crit":     "error",
	"warning":  "warn",
}

// cordonLevelRank orders the output groups. Unknown sorts last.
var cordonLevelRank = map[string]int{
	"error": 5, "fatal": 5, "warn": 4, "info": 3, "debug": 2, "trace": 1, "unknown": 0,
}

// cordonLevel extracts the severity of one line.
//
// The structured branch is deliberately strict: a value that is neither a
// normalisable alias nor a rank we know becomes "unknown" rather than
// being passed through as its own level. That keeps a field like
// `level: notalevel` from creating a phantom severity that sorts at rank
// 0 anyway — and note it does NOT then fall through to the regex, so a
// record with a junk level field reports unknown even if the word "error"
// appears elsewhere in it. Both behaviours are the Python's.
func cordonLevel(line string) string {
	if obj := cordonTryJSON(line); obj != nil {
		for _, k := range []string{"level", "detected_level", "severity", "lvl"} {
			v, ok := cordonFlatGet(obj, k)
			if !ok || v == "" {
				continue
			}
			lvl := strings.ToLower(strings.TrimSpace(v))
			if norm, isAlias := cordonLevelNormalise[lvl]; isAlias {
				return norm
			}
			if _, known := cordonLevelRank[lvl]; known {
				return lvl
			}
			return "unknown"
		}
	}
	m := cordonLevelRE.FindStringSubmatch(line)
	if m == nil {
		return "unknown"
	}
	lvl := strings.ToLower(m[1])
	if norm, ok := cordonLevelNormalise[lvl]; ok {
		return norm
	}
	return lvl
}

// cordonKeyToken matches the load-bearing tokens preserved verbatim in a
// group's keys= field: paths, HTTP status codes, error codes, versions.
// Go's RE2 has no alternation-order guarantee difference from Python here
// because the branches are mutually exclusive on their first character
// class, except for the Windows-path branch, which must stay first.
var cordonKeyToken = regexp.MustCompile(
	`(?:[A-Za-z]:[\\/][^\s"']+)` + // Windows path
		`|(?:/[^\s"']{2,})` + // Unix path
		`|(?:\b\d{3}\b)` + // HTTP status code
		`|(?:\b[A-Z]{2,}\d+\b)` + // error codes like E001, ERR42
		`|(?:\bv?\d+\.\d+\.\d+(?:-[A-Za-z0-9.+-]+)?\b)`) // versions

func cordonExtractKeys(line string) []string {
	return cordonKeyToken.FindAllString(line, -1)
}
