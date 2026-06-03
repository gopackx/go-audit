package audit

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// redactHeaders returns a copy of h with values replaced by redactedMarker
// whenever the header key (case-insensitively) appears in keys. Returns nil
// when h is empty so the caller can skip persistence.
func redactHeaders(h map[string]string, keys []string) map[string]string {
	if len(h) == 0 {
		return nil
	}
	set := map[string]struct{}{}
	for _, k := range keys {
		set[strings.ToLower(k)] = struct{}{}
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if _, hit := set[strings.ToLower(k)]; hit {
			out[k] = redactedMarker
		} else {
			out[k] = v
		}
	}
	return out
}

// redactBody walks body and replaces any map entry whose key appears in
// fields (case-insensitive) with redactedMarker. Scalars are returned
// unchanged; slices are walked recursively. Structs and other non-map
// payloads are normalized through a JSON round-trip first so callers can
// pass typed request/response objects (per README example) without losing
// redaction coverage.
func redactBody(body any, fields []string) any {
	if body == nil || len(fields) == 0 {
		return body
	}
	set := map[string]struct{}{}
	for _, k := range fields {
		set[strings.ToLower(k)] = struct{}{}
	}
	switch body.(type) {
	case map[string]any, []any:
		return redactWalk(body, set)
	case string, []byte, json.RawMessage,
		bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		// Scalars can't contain redactable keys.
		return body
	}
	// Structs, pointers, named maps, etc.: marshal → unmarshal into generic
	// types so redactWalk can see the field names. If marshal fails we fall
	// back to the original value rather than dropping the body entirely.
	b, err := json.Marshal(body)
	if err != nil {
		return body
	}
	var generic any
	if err := json.Unmarshal(b, &generic); err != nil {
		return body
	}
	return redactWalk(generic, set)
}

func redactWalk(v any, fields map[string]struct{}) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if _, hit := fields[strings.ToLower(k)]; hit {
				out[k] = redactedMarker
				continue
			}
			out[k] = redactWalk(val, fields)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactWalk(val, fields)
		}
		return out
	default:
		return v
	}
}

// truncateJSON returns a JSON document that replaces oversized payloads with a
// structured envelope so the resulting column value stays valid JSON.
//
//	{"_truncated": true, "original_size": 12345, "preview": "..."}
func truncateJSON(b []byte, max int) ([]byte, error) {
	if max <= 0 || len(b) <= max {
		return b, nil
	}
	// Cap the preview to max bytes, rewound to a rune boundary so we never
	// emit invalid UTF-8 inside the string literal.
	previewEnd := max
	if previewEnd > len(b) {
		previewEnd = len(b)
	}
	for previewEnd > 0 && !utf8.RuneStart(b[previewEnd-1]) && !utf8.Valid(b[:previewEnd]) {
		previewEnd--
	}
	return json.Marshal(struct {
		Truncated    bool   `json:"_truncated"`
		OriginalSize int    `json:"original_size"`
		Preview      string `json:"preview"`
	}{
		Truncated:    true,
		OriginalSize: len(b),
		Preview:      string(b[:previewEnd]),
	})
}
