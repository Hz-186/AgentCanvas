package toolruntime

import "testing"

func TestEffectiveLimit(t *testing.T) {
	for _, test := range []struct{ value, ceiling, want int }{{0, 30, 30}, {60, 30, 30}, {10, 30, 10}, {10, 0, 10}} {
		if got := EffectiveLimit(test.value, test.ceiling); got != test.want {
			t.Fatalf("EffectiveLimit(%d, %d) = %d, want %d", test.value, test.ceiling, got, test.want)
		}
	}
}

func TestValidateAllowedHostsNormalizesURLs(t *testing.T) {
	if err := ValidateAllowedHosts([]string{"HTTPS://API.EXAMPLE.COM:443/v1"}, []string{"api.example.com"}, false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAllowedHosts([]string{"api.example.com"}, nil, true); err == nil {
		t.Fatal("deny-all policy accepted a tool host")
	}
}
