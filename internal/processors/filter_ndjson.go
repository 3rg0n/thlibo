package processors

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// ndjson-filter: compress NDJSON / structured-log streams.
// Native Go port of processors/ndjson-filter/run.py (ADR 0010). Behaviour
// parity is locked by golden tests against the Python reference.
//
// Reads lines, parses each as JSON, groups by (level, msg) signature,
// deduplicates with a count multiplier, and emits sorted with errors first.
// Non-JSON lines pass through unchanged (lossless contract).

func init() { RegisterNative("ndjson-filter", ndjsonFilter) }

// levelRank maps log level strings (case-insensitive) to a numeric rank
// for sorting. Higher rank = higher priority (errors before warnings).
// Aliases are normalized to their common representative.
var levelRank = map[string]int{
	"fatal":    5,
	"panic":    5,
	"error":    4,
	"err":      4,
	"critical": 4,
	"crit":     4,
	"warn":     3,
	"warning":  3,
	"info":     2,
	"notice":   2,
	"debug":    1,
	"trace":    0,
}

// extractLevel reads a normalised level string from a structured-log record.
// Handles common synonyms (level, severity, lvl, severity_text, loglevel).
// Returns "info" (rank 2) as the default when no level field is present.
func extractLevel(rec map[string]any) string {
	for _, k := range []string{"level", "severity", "lvl", "severity_text", "loglevel"} {
		v, ok := rec[k]
		if !ok {
			continue
		}
		// String field: use directly (lowercased).
		if s, ok := v.(string); ok && s != "" {
			return strings.ToLower(s)
		}
		// OTel numeric severity 1-24.
		if num, ok := v.(float64); ok {
			if num >= 17 {
				return "fatal"
			}
			if num >= 13 {
				return "error"
			}
			if num >= 9 {
				return "warn"
			}
			if num >= 5 {
				return "info"
			}
			return "debug"
		}
	}
	return "info"
}

// extractMsg reads the message field from a record.
// Common keys: msg, message, body, log, text.
func extractMsg(rec map[string]any) string {
	for _, k := range []string{"msg", "message", "body", "log", "text"} {
		if v, ok := rec[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

// HTTP access-log field synonyms. Traefik, nginx, envoy, k8s ingress,
// and app request logs all log every request with a constant level+msg
// (often level:"info", msg:""), so a (level,msg) signature collapses the
// whole stream to one row and drops every path/status/method distinction
// (#27). extractMethod/Status/Path pull the fields that actually vary so
// they join the signature. All return "" when absent — a record with no
// HTTP fields (a generic log line) contributes empty components and the
// signature reduces to (level,msg), preserving prior behaviour exactly.

// extractHTTPField returns the first present string/number field among
// the given synonym keys, stringified. Empty when none present.
func extractHTTPField(rec map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := rec[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case string:
			if t != "" {
				return t
			}
		case float64:
			// Status codes and the like arrive as JSON numbers.
			return strconv.Itoa(int(t))
		}
	}
	return ""
}

func extractMethod(rec map[string]any) string {
	return extractHTTPField(rec, "method", "RequestMethod", "http_method", "verb", "requestMethod")
}

// extractStatusClass returns the status class (e.g. "5xx") rather than the
// exact code, so /foo returning 500 vs 503 still collapse — the class is
// what a "which paths 5xx'd" question needs, and it keeps compression high.
func extractStatusClass(rec map[string]any) string {
	s := extractHTTPField(rec, "status", "DownstreamStatus", "http_status", "statusCode", "status_code", "response_code", "OriginStatus")
	if s == "" {
		return ""
	}
	// Reduce a 3-digit code to its class ("500" -> "5xx"); leave anything
	// else (already a class, or non-numeric) as-is.
	if len(s) == 3 && s[0] >= '1' && s[0] <= '5' {
		allDigits := true
		for i := 0; i < 3; i++ {
			if s[i] < '0' || s[i] > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return string(s[0]) + "xx"
		}
	}
	return s
}

func extractPathShape(rec map[string]any) string {
	p := extractHTTPField(rec, "RequestPath", "path", "url", "uri", "target", "http_path", "request_uri", "requestPath")
	if p == "" {
		return ""
	}
	return pathShape(p)
}

// pathShape normalises a URL path into a template by replacing high-
// cardinality segments (numeric IDs, UUIDs, long hex/tokens) with a
// placeholder, so /api/users/1007 and /api/users/1008 share a shape while
// /api/users and /api/orders stay distinct. This is what keeps the "300
// requests -> ~N distinct route+status groups" compression meaningful
// without dropping the route distribution.
func pathShape(p string) string {
	// Trim query string / fragment — they're per-request high-cardinality.
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if s == "" {
			continue
		}
		if segLooksVariable(s) {
			segs[i] = "<var>"
		}
	}
	return strings.Join(segs, "/")
}

// segLooksVariable reports whether a path segment is a high-cardinality
// value (a numeric id, a uuid, or a long hex/opaque token) rather than a
// stable route word.
func segLooksVariable(s string) bool {
	// All digits -> numeric id.
	allDigits := true
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return true
	}
	// UUID (8-4-4-4-12 hex).
	if len(s) == 36 && s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-' {
		return true
	}
	// Long hex/opaque token (>=24 chars, hex-ish) — commit SHAs, ids.
	if len(s) >= 24 {
		hexish := true
		for i := 0; i < len(s); i++ {
			c := s[i]
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				hexish = false
				break
			}
		}
		if hexish {
			return true
		}
	}
	return false
}

// jsonFieldOrder extracts field names in the order they appear in a JSON string.
func jsonFieldOrder(jsonStr string) []string {
	var order []string
	var inString bool
	var escaped bool
	var currentKey strings.Builder

	for i := 0; i < len(jsonStr); i++ {
		c := jsonStr[i]

		if escaped {
			escaped = false
			if inString {
				currentKey.WriteByte(c)
			}
			continue
		}

		if c == '\\' && inString {
			escaped = true
			if inString {
				currentKey.WriteByte(c)
			}
			continue
		}

		if c == '"' {
			inString = !inString
			if inString {
				currentKey.Reset()
			} else {
				// End of string; check if this is a key
				if i+1 < len(jsonStr) {
					j := i + 1
					for j < len(jsonStr) && (jsonStr[j] == ' ' || jsonStr[j] == ':') {
						if jsonStr[j] == ':' {
							// This was a key
							order = append(order, currentKey.String())
							break
						}
						j++
					}
				}
			}
			continue
		}

		if inString {
			currentKey.WriteByte(c)
		}
	}

	return order
}

// orderedJSONString rebuilds a JSON object string preserving field order, optionally adding _count.
func orderedJSONString(jsonStr string, rec map[string]any, count int) (string, error) {
	// Parse the original JSON to get field order.
	order := jsonFieldOrder(jsonStr)
	seen := make(map[string]bool)

	var buf bytes.Buffer
	buf.WriteString("{")

	var b []byte
	first := true
	// First emit fields in original order.
	for _, key := range order {
		if seen[key] {
			continue
		}
		seen[key] = true

		if val, ok := rec[key]; ok {
			if !first {
				buf.WriteString(",")
			}
			first = false

			// Marshal key
			b, _ = json.Marshal(key)
			buf.Write(b)
			buf.WriteString(":")

			// Marshal value
			b, _ = json.Marshal(val)
			buf.Write(b)
		}
	}

	// Then emit any new fields (e.g., _count).
	for key, val := range rec {
		if !seen[key] {
			if !first {
				buf.WriteString(",")
			}
			first = false

			// Marshal key
			b, _ = json.Marshal(key)
			buf.Write(b)
			buf.WriteString(":")

			// Marshal value
			b, _ = json.Marshal(val)
			buf.Write(b)

			seen[key] = true
		}
	}

	buf.WriteString("}")
	return buf.String(), nil
}

func ndjsonFilter(raw []byte) []byte {
	// Normalize line endings and split (Python splitlines semantics).
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	// Drop trailing empty element from split (matches Python splitlines()).
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	// Group by (level, msg, method, statusClass, pathShape). The extra
	// HTTP fields keep access-log records distinct so route/status
	// distribution survives (#27); they're empty for non-HTTP records, so
	// the signature reduces to (level, msg) and generic logs behave as
	// before. Value is [count, firstRecordJSON, firstLineStr].
	type groupValue struct {
		count   int
		rec     map[string]any
		lineStr string
	}
	groups := make(map[[5]string]groupValue)
	var order [][5]string // group keys in first-seen order (Python dict parity)
	var nonJSON []string

	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			continue
		}

		var rec map[string]any
		err := json.Unmarshal([]byte(stripped), &rec)
		if err != nil || rec == nil {
			// Not JSON or not a dict — pass through unchanged.
			nonJSON = append(nonJSON, line)
			continue
		}

		level := extractLevel(rec)
		msg := extractMsg(rec)
		key := [5]string{
			level,
			msg,
			extractMethod(rec),
			extractStatusClass(rec),
			extractPathShape(rec),
		}

		if gv, exists := groups[key]; exists {
			gv.count++
			groups[key] = gv
		} else {
			groups[key] = groupValue{count: 1, rec: rec, lineStr: stripped}
			order = append(order, key) // first-seen order
		}
	}

	if len(groups) == 0 {
		// Nothing parseable — pass through unchanged.
		return raw
	}

	// Sort group entries by (level rank descending, original arrival).
	// Build a slice of (key, value) pairs and sort by rank.
	type kvPair struct {
		key   [5]string
		value groupValue
	}
	// Iterate in first-seen order (not Go's random map order) so the
	// stable sort's secondary key is deterministic arrival order —
	// matching Python's insertion-ordered dict for byte parity.
	var items []kvPair
	for _, k := range order {
		items = append(items, kvPair{k, groups[k]})
	}

	sort.SliceStable(items, func(i, j int) bool {
		rankI := levelRank[items[i].key[0]]
		rankJ := levelRank[items[j].key[0]]
		// Descending: higher rank first.
		return rankI > rankJ
	})

	var out []string
	for _, item := range items {
		gv := item.value
		rec := gv.rec
		count := gv.count
		lineStr := gv.lineStr

		if count > 1 {
			rec["_count"] = count
		}

		// Preserve field order from the original JSON.
		jsonStr, _ := orderedJSONString(lineStr, rec, count)
		out = append(out, jsonStr)
	}

	// Append any non-JSON lines, separated by a blank line.
	if len(nonJSON) > 0 {
		out = append(out, "")
		out = append(out, nonJSON...)
	}

	result := strings.Join(out, "\n")
	// Match Python: trailing newline only if input had one.
	if strings.HasSuffix(string(raw), "\n") {
		result += "\n"
	}

	return []byte(result)
}
