package version

import "testing"

func TestVersionString(t *testing.T) {
	if got := Version().String(); got != "dev" {
		t.Fatalf("unstamped version = %q, want dev", got)
	}
	version, commit, date = "0.1.0", "abc123", "2026-09-29"
	t.Cleanup(func() { version, commit, date = "dev", "", "" })
	if got := Version().String(); got != "0.1.0 (abc123, 2026-09-29)" {
		t.Fatalf("stamped version = %q", got)
	}
}
