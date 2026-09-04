//go:build darwin

package libpython

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"modernc.org/libc"
)

type cFormatSpec struct {
	flags     string
	width     int
	hasWidth  bool
	precision int
	hasPrec   bool
	length    string
	verb      byte
}

func parseCFormat(format string, pos *int, ap *uintptr) cFormatSpec {
	s := cFormatSpec{}
	for *pos < len(format) && strings.ContainsRune("-+ #0", rune(format[*pos])) {
		if !strings.ContainsRune(s.flags, rune(format[*pos])) {
			s.flags += string(format[*pos])
		}
		*pos++
	}
	if *pos < len(format) && format[*pos] == '*' {
		s.width, s.hasWidth = int(libc.VaInt32(ap)), true
		*pos++
		if s.width < 0 {
			s.width = -s.width
			if !strings.Contains(s.flags, "-") {
				s.flags += "-"
			}
		}
	} else {
		start := *pos
		for *pos < len(format) && format[*pos] >= '0' && format[*pos] <= '9' {
			*pos++
		}
		if *pos > start {
			s.width, _ = strconv.Atoi(format[start:*pos])
			s.hasWidth = true
		}
	}
	if *pos < len(format) && format[*pos] == '.' {
		*pos++
		s.hasPrec = true
		if *pos < len(format) && format[*pos] == '*' {
			s.precision = int(libc.VaInt32(ap))
			*pos++
			if s.precision < 0 {
				s.hasPrec = false
			}
		} else {
			start := *pos
			for *pos < len(format) && format[*pos] >= '0' && format[*pos] <= '9' {
				*pos++
			}
			if *pos > start {
				s.precision, _ = strconv.Atoi(format[start:*pos])
			}
		}
	}
	for _, length := range []string{"hh", "ll", "h", "l", "j", "z", "t", "L"} {
		if strings.HasPrefix(format[*pos:], length) {
			s.length = length
			*pos += len(length)
			break
		}
	}
	if *pos < len(format) {
		s.verb = format[*pos]
		*pos++
	}
	return s
}

func (s cFormatSpec) goFormat(verb byte) string {
	var b strings.Builder
	b.WriteByte('%')
	b.WriteString(s.flags)
	if s.hasWidth {
		b.WriteString(strconv.Itoa(s.width))
	}
	if s.hasPrec {
		b.WriteByte('.')
		b.WriteString(strconv.Itoa(s.precision))
	}
	b.WriteByte(verb)
	return b.String()
}

func (s cFormatSpec) signedArg(ap *uintptr) int64 {
	v := libc.VaInt64(ap)
	switch s.length {
	case "hh":
		return int64(int8(v))
	case "h":
		return int64(int16(v))
	case "":
		return int64(int32(v))
	default:
		return v
	}
}

func (s cFormatSpec) unsignedArg(ap *uintptr) uint64 {
	v := uint64(libc.VaInt64(ap))
	switch s.length {
	case "hh":
		return uint64(uint8(v))
	case "h":
		return uint64(uint16(v))
	case "":
		return uint64(uint32(v))
	default:
		return v
	}
}

func cFormat(format string, va uintptr) []byte {
	var out strings.Builder
	ap := va
	for i := 0; i < len(format); {
		if format[i] != '%' {
			out.WriteByte(format[i])
			i++
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			out.WriteByte('%')
			i++
			continue
		}
		s := parseCFormat(format, &i, &ap)
		switch s.verb {
		case 'd', 'i':
			out.WriteString(fmt.Sprintf(s.goFormat('d'), s.signedArg(&ap)))
		case 'u':
			out.WriteString(fmt.Sprintf(s.goFormat('d'), s.unsignedArg(&ap)))
		case 'o', 'x', 'X':
			out.WriteString(fmt.Sprintf(s.goFormat(s.verb), s.unsignedArg(&ap)))
		case 'c':
			out.WriteString(fmt.Sprintf(s.goFormat('c'), byte(libc.VaInt32(&ap))))
		case 's':
			p := libc.VaUintptr(&ap)
			value := "(null)"
			if p != 0 {
				value = libc.GoString(p)
			}
			if s.hasPrec && len(value) > s.precision {
				value = value[:s.precision]
			}
			s.hasPrec = false
			out.WriteString(fmt.Sprintf(s.goFormat('s'), value))
		case 'p':
			p := libc.VaUintptr(&ap)
			text := "0x" + strconv.FormatUint(uint64(p), 16)
			s.hasPrec = false
			out.WriteString(fmt.Sprintf(s.goFormat('s'), text))
		case 'f', 'F', 'e', 'E', 'g', 'G', 'a', 'A':
			verb := s.verb
			if verb == 'a' {
				verb = 'x'
			} else if verb == 'A' {
				verb = 'X'
			}
			if !s.hasPrec {
				s.hasPrec = true
				s.precision = 6
			}
			out.WriteString(fmt.Sprintf(s.goFormat(verb), libc.VaFloat64(&ap)))
		case 'n':
			p := libc.VaUintptr(&ap)
			if p != 0 {
				n := out.Len()
				switch s.length {
				case "hh":
					*(*int8)(unsafe.Pointer(p)) = int8(n)
				case "h":
					*(*int16)(unsafe.Pointer(p)) = int16(n)
				case "":
					*(*int32)(unsafe.Pointer(p)) = int32(n)
				default:
					*(*int64)(unsafe.Pointer(p)) = int64(n)
				}
			}
		default:
			out.WriteByte('%')
			if s.verb != 0 {
				out.WriteByte(s.verb)
			}
		}
	}
	return []byte(out.String())
}

func writeCFormat(tls *libc.TLS, stream uintptr, format, va uintptr) int32 {
	b := cFormat(libc.GoString(format), va)
	if len(b) == 0 {
		return 0
	}
	p := libc.Xmalloc(tls, uint64(len(b)))
	if p == 0 {
		return -1
	}
	defer libc.Xfree(tls, p)
	copy(cBytes(p, uint64(len(b))), b)
	libc.Xfwrite(tls, p, 1, uint64(len(b)), stream)
	return int32(len(b))
}

func _ccgo_fprintf(tls *libc.TLS, stream, format, va uintptr) int32 {
	return writeCFormat(tls, stream, format, va)
}

func _ccgo_printf(tls *libc.TLS, format, va uintptr) int32 {
	return writeCFormat(tls, libc.X__stdoutp, format, va)
}

func _ccgo_vfprintf(tls *libc.TLS, stream, format, va uintptr) int32 {
	return writeCFormat(tls, stream, format, va)
}

func _ccgo___builtin___vsnprintf_chk(tls *libc.TLS, dst uintptr, size uint64, flag int32, objectSize uint64, format, va uintptr) int32 {
	b := cFormat(libc.GoString(format), va)
	if size != 0 && dst != 0 {
		n := uint64(len(b))
		if n >= size {
			n = size - 1
		}
		copy(cBytes(dst, n), b[:n])
		*(*byte)(unsafe.Pointer(dst + uintptr(n))) = 0
	}
	return int32(len(b))
}

func _ccgo___builtin___snprintf_chk(tls *libc.TLS, dst uintptr, size uint64, flag int32, objectSize uint64, format, va uintptr) int32 {
	return _ccgo___builtin___vsnprintf_chk(tls, dst, size, flag, objectSize, format, va)
}

func _ccgo___builtin___sprintf_chk(tls *libc.TLS, dst uintptr, flag int32, objectSize uint64, format, va uintptr) int32 {
	b := cFormat(libc.GoString(format), va)
	copy(cBytes(dst, uint64(len(b))), b)
	*(*byte)(unsafe.Pointer(dst + uintptr(len(b)))) = 0
	return int32(len(b))
}
