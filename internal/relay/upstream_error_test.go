package relay

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadUpstreamErrorBodyIsBounded(t *testing.T) {
	input := bytes.Repeat([]byte("x"), errBodySnippetLimit*4)
	got, err := readUpstreamErrorBody(bytes.NewReader(input))
	if err != nil {
		t.Fatalf("readUpstreamErrorBody: %v", err)
	}
	if len(got) <= errBodySnippetLimit {
		t.Fatalf("expected truncation marker, got %d bytes", len(got))
	}
	if !strings.HasSuffix(string(got), "...[truncated]") {
		t.Fatalf("missing truncation marker: %q", string(got[len(got)-32:]))
	}
	if len(got) > errBodySnippetLimit+len("...[truncated]") {
		t.Fatalf("body exceeds diagnostic budget: %d", len(got))
	}
}
