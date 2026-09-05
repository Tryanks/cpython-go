package libpython

import "testing"

func TestUTF16String(t *testing.T) {
	tests := []struct {
		name  string
		input []uint16
		want  string
	}{
		{name: "empty", input: nil, want: ""},
		{name: "ascii", input: []uint16{'c', 'p', '3', '1', '4'}, want: "cp314"},
		{name: "surrogate pair", input: []uint16{0xd83d, 0xde00}, want: "😀"},
		{name: "terminator", input: []uint16{'a', 0, 'b'}, want: "a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := utf16String(test.input); got != test.want {
				t.Fatalf("utf16String(%#v) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}
