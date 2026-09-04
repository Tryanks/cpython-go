//go:build darwin

package libpython

import (
	"testing"
	"unsafe"

	"modernc.org/libc"
)

func TestCFormat(t *testing.T) {
	stringArg := func(s string) uintptr {
		p, err := libc.CString(s)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { libc.Xfree(nil, p) })
		return p
	}
	cases := []struct {
		name   string
		format string
		args   []any
		want   string
	}{
		{"zd", "%zd", []any{int64(-42)}, "-42"},
		{"integer precision", "%.3d", []any{int32(7)}, "007"},
		{"float width precision", "%5.2f", []any{1.25}, " 1.25"},
		{"left string", "%-8s|", []any{stringArg("go")}, "go      |"},
		{"alternate hex", "%#x", []any{uint32(42)}, "0x2a"},
		{"zero float", "%08.3f", []any{1.25}, "0001.250"},
		{"percent", "100%%", nil, "100%"},
		{"character", "%c", []any{int32('A')}, "A"},
		{"pointer", "%p", []any{uintptr(0x1234)}, "0x1234"},
		{"long long", "%lld", []any{int64(-9223372036854775807)}, "-9223372036854775807"},
		{"star string", "%.*s", []any{int32(3), stringArg("abcdef")}, "abc"},
		{"exponent", "%.2e", []any{12.5}, "1.25e+01"},
		{"general", "%.4g", []any{12.345}, "12.35"},
		{"signed plus", "%+d", []any{int32(9)}, "+9"},
		{"signed space", "% d", []any{int32(9)}, " 9"},
		{"width star", "%*d", []any{int32(5), int32(12)}, "   12"},
		{"negative width", "%*d", []any{int32(-5), int32(12)}, "12   "},
		{"precision star", "%.*f", []any{int32(1), 2.25}, "2.2"},
		{"unsigned", "%u", []any{uint32(4294967295)}, "4294967295"},
		{"octal", "%#o", []any{uint32(9)}, "011"},
		{"upper hex", "%X", []any{uint32(0xbeef)}, "BEEF"},
		{"short", "%hd", []any{int32(65535)}, "-1"},
		{"byte unsigned", "%hhu", []any{uint32(257)}, "1"},
		{"string precision", "%.4s", []any{stringArg("python")}, "pyth"},
		{"multiple", "%s:%d", []any{stringArg("x"), int32(3)}, "x:3"},
		{"upper exponent", "%.1E", []any{10.0}, "1.0E+01"},
		{"hex float", "%.2a", []any{3.5}, "0x1.c0p+01"},
		{"default float precision", "%f", []any{1.0}, "1.000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var va uintptr
			if len(tc.args) != 0 {
				va = libc.NewVaList(tc.args...)
				defer libc.Xfree(nil, va)
			}
			if got := string(cFormat(tc.format, va)); got != tc.want {
				t.Fatalf("cFormat(%q) = %q, want %q", tc.format, got, tc.want)
			}
		})
	}
}

func TestCFormatCount(t *testing.T) {
	count := libc.Xmalloc(nil, 4)
	defer libc.Xfree(nil, count)
	va := libc.NewVaList(int32(12), count)
	defer libc.Xfree(nil, va)
	if got := string(cFormat("ab%d%ncd", va)); got != "ab12cd" {
		t.Fatalf("cFormat count output = %q", got)
	}
	if got := *(*int32)(unsafe.Pointer(count)); got != 4 {
		t.Fatalf("%%n count = %d, want 4", got)
	}
}
