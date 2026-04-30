package logutil

import "testing"

func TestNormalizeLevel(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "debug", raw: "debug", want: "debug"},
		{name: "warn uppercase", raw: "WARN", want: "warn"},
		{name: "error spaces", raw: " error ", want: "error"},
		{name: "invalid fallback", raw: "verbose", want: "info"},
		{name: "empty fallback", raw: "", want: "info"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeLevel(tt.raw); got != tt.want {
				t.Fatalf("NormalizeLevel(%q)=%q want=%q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestSetLevelAndEnabled(t *testing.T) {
	SetLevel("warn")
	t.Cleanup(func() { SetLevel("info") })

	if Enabled(LevelInfo) {
		t.Fatalf("info should be disabled at warn level")
	}
	if !Enabled(LevelWarn) {
		t.Fatalf("warn should be enabled at warn level")
	}
	if !Enabled(LevelError) {
		t.Fatalf("error should be enabled at warn level")
	}
}
