package pager

import "testing"

func TestLabel(t *testing.T) {
	for in, want := range map[string]string{"irm": "IRM", "": "IRM", "pagerduty": "pagerduty"} {
		if got := Label(in); got != want {
			t.Errorf("Label(%q) = %q, want %q", in, got, want)
		}
	}
}
