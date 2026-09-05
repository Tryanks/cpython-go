//go:build darwin || linux

package libpython

import "testing"

func TestLocaleCollationKey(t *testing.T) {
	tests := []struct {
		a, b string
		want int32
	}{
		{"a", "b", -1},
		{"à", "b", -1},
		{"É", "f", -1},
		{"ångström", "banana", -1},
		{"z", "Ž", -1},
		{"same", "same", 0},
	}
	for _, tt := range tests {
		if got := compareCollation([]rune(tt.a), []rune(tt.b)); got != tt.want {
			t.Errorf("compareCollation(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
