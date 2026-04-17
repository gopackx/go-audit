package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTruncateJSON_NoTruncationNeeded(t *testing.T) {
	in := []byte(`{"a":1}`)
	out, err := truncateJSON(in, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(in) {
		t.Fatalf("expected passthrough, got %s", out)
	}
}

func TestTruncateJSON_ProducesValidJSON(t *testing.T) {
	// Raw bytes contain quotes and backslashes that would break naive wrapping.
	in := []byte(`{"payload":"he said \"hi\" and gave path C:\\tmp"}`)
	out, err := truncateJSON(in, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Truncated    bool   `json:"_truncated"`
		OriginalSize int    `json:"original_size"`
		Preview      string `json:"preview"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (got %s)", err, out)
	}
	if !got.Truncated {
		t.Fatalf("_truncated should be true")
	}
	if got.OriginalSize != len(in) {
		t.Fatalf("original_size = %d, want %d", got.OriginalSize, len(in))
	}
	if got.Preview == "" {
		t.Fatalf("preview should not be empty")
	}
}

func TestTruncateJSON_UTF8Boundary(t *testing.T) {
	// 3-byte rune split across the max boundary.
	in := []byte(`"` + strings.Repeat("あ", 20) + `"`) // each 'あ' is 3 bytes
	out, err := truncateJSON(in, 7)                   // lands mid-rune
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Preview string `json:"preview"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not valid JSON: %v (got %s)", err, out)
	}
	// Preview must not contain invalid UTF-8 — Unmarshal would have failed
	// otherwise, but double-check via range.
	for _, r := range got.Preview {
		if r == '\uFFFD' {
			t.Fatalf("preview contains replacement char (invalid UTF-8): %q", got.Preview)
		}
	}
}
