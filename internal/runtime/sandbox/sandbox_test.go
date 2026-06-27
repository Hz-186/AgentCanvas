package sandbox

import "testing"

func TestLimitedBufferTruncatesOutput(t *testing.T) {
	buf := &limitedBuffer{limit: 5}
	n, err := buf.Write([]byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if n != len("hello world") {
		t.Fatalf("Write() n = %d", n)
	}
	if got := buf.String(); got != "hello" {
		t.Fatalf("buffer = %q", got)
	}
	if !buf.truncated {
		t.Fatal("expected truncated flag")
	}
}
