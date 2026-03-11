package bazelmcp

import "testing"

// TestLimitedBufferConsumesFullWriteWhenTruncating verifies limitedBuffer honors Write contract when truncating.
func TestLimitedBufferConsumesFullWriteWhenTruncating(t *testing.T) {
	buffer := limitedBuffer{Limit: 4}

	written, err := buffer.Write([]byte("abcdef"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if written != 6 {
		t.Fatalf("unexpected write count: got %d want 6", written)
	}
	if got := buffer.String(); got != "abcd" {
		t.Fatalf("unexpected buffer contents: got %q want %q", got, "abcd")
	}
	if !buffer.Truncated {
		t.Fatal("expected buffer to be marked truncated")
	}
}
