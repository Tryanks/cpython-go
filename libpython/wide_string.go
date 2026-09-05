package libpython

import "unicode/utf16"

func utf16String(value []uint16) string {
	for i, unit := range value {
		if unit == 0 {
			value = value[:i]
			break
		}
	}
	return string(utf16.Decode(value))
}
