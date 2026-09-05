// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

//go:build darwin || linux

package libpython

import (
	"strings"
	"unicode"
	"unsafe"

	"modernc.org/libc"
)

// localeCollationKey approximates en_US.UTF-8 collation in three levels:
// base letters (case-insensitive), accents, then case.  Its decomposition
// table intentionally stops at Latin-1 and Latin Extended-A; characters
// outside that range retain ordinal weights.
func localeCollationKey(s []rune) []rune {
	primary := make([]rune, 0, len(s))
	accents := make([]rune, 0, len(s))
	cases := make([]rune, 0, len(s))
	for _, r := range s {
		base, accent := latinCollationRune(r)
		primary = append(primary, unicode.ToLower(base)+2)
		accents = append(accents, accent+2)
		if unicode.IsUpper(r) {
			cases = append(cases, 3)
		} else {
			cases = append(cases, 2)
		}
	}
	key := append(primary, 1)
	key = append(key, accents...)
	key = append(key, 1)
	return append(key, cases...)
}

func latinCollationRune(r rune) (rune, rune) {
	groups := [...]struct {
		base  rune
		chars string
	}{
		{'a', "ÀÁÂÃÄÅàáâãäåĀāĂăĄą"},
		{'c', "ÇçĆćĈĉĊċČč"},
		{'d', "ĎďĐđ"},
		{'e', "ÈÉÊËèéêëĒēĔĕĖėĘęĚě"},
		{'g', "ĜĝĞğĠġĢģ"},
		{'h', "ĤĥĦħ"},
		{'i', "ÌÍÎÏìíîïĨĩĪīĬĭĮįİı"},
		{'j', "Ĵĵ"},
		{'k', "Ķķĸ"},
		{'l', "ĹĺĻļĽľĿŀŁł"},
		{'n', "ÑñŃńŅņŇňŉŊŋ"},
		{'o', "ÒÓÔÕÖØòóôõöøŌōŎŏŐő"},
		{'r', "ŔŕŖŗŘř"},
		{'s', "ŚśŜŝŞşŠšſ"},
		{'t', "ŢţŤťŦŧ"},
		{'u', "ÙÚÛÜùúûüŨũŪūŬŭŮůŰűŲų"},
		{'w', "Ŵŵ"},
		{'y', "ÝýÿŶŷŸ"},
		{'z', "ŹźŻżŽž"},
	}
	for _, group := range groups {
		if i := strings.IndexRune(group.chars, r); i >= 0 {
			// The exact secondary ordering is less important than keeping all
			// accents behind their common primary base. Use the rune itself as
			// a stable secondary weight.
			return group.base, unicode.ToLower(r)
		}
	}
	return r, 0
}

func compareCollation(a, b []rune) int32 {
	ka, kb := localeCollationKey(a), localeCollationKey(b)
	for i := 0; i < len(ka) && i < len(kb); i++ {
		if ka[i] < kb[i] {
			return -1
		}
		if ka[i] > kb[i] {
			return 1
		}
	}
	if len(ka) < len(kb) {
		return -1
	}
	if len(ka) > len(kb) {
		return 1
	}
	return 0
}

func _ccgo_strcoll(tls *libc.TLS, a, b uintptr) int32 {
	return compareCollation([]rune(libc.GoString(a)), []rune(libc.GoString(b)))
}

func _ccgo_wcscoll(tls *libc.TLS, a, b uintptr) int32 {
	return compareCollation(wideRunes(a), wideRunes(b))
}

func _ccgo_strxfrm(tls *libc.TLS, dst, src uintptr, n uint64) uint64 {
	key := []byte(string(localeCollationKey([]rune(libc.GoString(src)))))
	if dst != 0 && n != 0 {
		m := len(key)
		if m >= int(n) {
			m = int(n) - 1
		}
		copy(cBytes(dst, n), key[:m])
		*(*byte)(unsafe.Pointer(dst + uintptr(m))) = 0
	}
	return uint64(len(key))
}

func _ccgo_wcsxfrm(tls *libc.TLS, dst, src uintptr, n uint64) uint64 {
	key := localeCollationKey(wideRunes(src))
	if dst != 0 && n != 0 {
		m := len(key)
		if m >= int(n) {
			m = int(n) - 1
		}
		copy(unsafe.Slice((*int32)(unsafe.Pointer(dst)), int(n)), rune32s(key[:m]))
		*(*int32)(unsafe.Pointer(dst + uintptr(m)*4)) = 0
	}
	return uint64(len(key))
}

func rune32s(runes []rune) []int32 {
	return *(*[]int32)(unsafe.Pointer(&runes))
}
