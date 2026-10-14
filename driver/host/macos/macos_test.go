package macos

import "testing"

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		`Safari`:     `"Safari"`,
		`say "hi"`:   `"say \"hi\""`,
		`back\slash`: `"back\\slash"`,
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSwitchDesktop_RejectsOutOfRange(t *testing.T) {
	for _, n := range []int{0, 10} {
		if err := System.SwitchDesktop(n); err == nil {
			t.Errorf("SwitchDesktop(%d) = nil, want an error", n)
		}
	}
}

func TestPermissionState_Has(t *testing.T) {
	s := PermissionState{Supported: true, Accessibility: true}
	if !s.Has(Accessibility) || s.Has(ScreenRecording) {
		t.Errorf("Has: got %v/%v, want true/false", s.Has(Accessibility), s.Has(ScreenRecording))
	}
}
