package gate

import (
	"strings"
	"testing"
)

func TestLimitWriterBoundsOutput(t *testing.T) {
	w := &limitWriter{limit: 4}
	input := "abcdefgh"
	n, err := w.Write([]byte(input))
	if err != nil || n != len(input) {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if got := w.String(); !strings.HasPrefix(got, "abcd") || !strings.Contains(got, "truncated") {
		t.Fatalf("String() = %q", got)
	}
}
