package provider

import "testing"

func TestMaskSecret(t *testing.T) {
	for input, want := range map[string]string{"": "", "short": "****", "123456789": "1234****6789"} {
		if got := MaskSecret(input); got != want {
			t.Fatalf("MaskSecret(%q) = %q, want %q", input, got, want)
		}
	}
}
