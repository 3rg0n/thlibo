package processors

import (
	"bytes"
	"encoding/json"
	"sort"
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

	// Group by (level, msg). Value is [count, firstRecordJSON, firstLineStr].
	type groupValue struct {
		count    int
		rec      map[string]any
		lineStr  string
	}
	groups := make(map[[2]string]groupValue)
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
		key := [2]string{level, msg}

		if gv, exists := groups[key]; exists {
			gv.count++
			groups[key] = gv
		} else {
			groups[key] = groupValue{count: 1, rec: rec, lineStr: stripped}
		}
	}

	if len(groups) == 0 {
		// Nothing parseable — pass through unchanged.
		return raw
	}

	// Sort group entries by (level rank descending, original arrival).
	// Build a slice of (key, value) pairs and sort by rank.
	type kvPair struct {
		key   [2]string
		value groupValue
	}
	var items []kvPair
	for k, v := range groups {
		items = append(items, kvPair{k, v})
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
