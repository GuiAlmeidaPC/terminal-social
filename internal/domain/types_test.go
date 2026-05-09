package domain

import "testing"

func TestSanitizeUserTextStripsTerminalControls(t *testing.T) {
	input := "a\x1b[31mred\x1b[0m \x1b]52;c;secret\x07clip \u009b31mc1\u009b0m"
	want := "ared clip c1"
	if got := SanitizeUserText(input); got != want {
		t.Fatalf("SanitizeUserText() = %q, want %q", got, want)
	}
}
