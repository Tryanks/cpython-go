//go:build darwin

package libpython

import (
	"bytes"
	"fmt"
	"testing"
	"time"
	"unsafe"

	"modernc.org/libc"
)

func TestDarwinLibcWideAndInetShims(t *testing.T) {
	tls := libc.NewTLS()
	walloc := func(s []rune) uintptr {
		p := libc.Xmalloc(tls, uint64((len(s)+1)*4))
		for i, r := range s {
			*(*int32)(unsafe.Pointer(p + uintptr(i)*4)) = int32(r)
		}
		return p
	}
	a := walloc([]rune("héllo"))
	b := walloc([]rune("héllp"))
	defer libc.Xfree(tls, a)
	defer libc.Xfree(tls, b)

	if got := _wcslen(tls, a); got != 5 {
		t.Fatalf("_wcslen = %d, want 5", got)
	}
	if got := _wcscmp(tls, a, b); got >= 0 {
		t.Fatalf("_wcscmp = %d, want negative", got)
	}
	if got := _ccgo_wcschr(tls, a, 'é'); got != a+4 {
		t.Fatalf("_ccgo_wcschr = %#x, want %#x", got, a+4)
	}

	mb := libc.Xmalloc(tls, 16)
	defer libc.Xfree(tls, mb)
	if got := _wcstombs(tls, mb, a, 16); got != 6 {
		t.Fatalf("_wcstombs = %d, want 6", got)
	}
	if got := libc.GoString(mb); got != "héllo" {
		t.Fatalf("_wcstombs bytes = %q, want %q", got, "héllo")
	}
	wc := libc.Xmalloc(tls, 4)
	defer libc.Xfree(tls, wc)
	if got := _mbrtowc(tls, wc, mb+1, 2, 0); got != 2 || *(*int32)(unsafe.Pointer(wc)) != 'é' {
		t.Fatalf("_mbrtowc = (%d, %U), want (2, U+00E9)", got, *(*int32)(unsafe.Pointer(wc)))
	}

	ipText := libc.Xmalloc(tls, 10)
	defer libc.Xfree(tls, ipText)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(ipText)), 10), "127.0.0.1\x00")
	ip := libc.Xmalloc(tls, 4)
	defer libc.Xfree(tls, ip)
	if got := _inet_aton(tls, ipText, ip); got != 1 {
		t.Fatalf("_inet_aton = %d, want 1", got)
	}
	if got := unsafe.Slice((*byte)(unsafe.Pointer(ip)), 4); !bytes.Equal(got, []byte{127, 0, 0, 1}) {
		t.Fatalf("_inet_aton bytes = %v", got)
	}
}

func TestDarwinStrftimeISOWeek(t *testing.T) {
	tls := libc.NewTLS()
	defer tls.Close()
	for _, date := range []time.Time{
		time.Date(2021, 1, 3, 12, 34, 56, 0, time.UTC),
		time.Date(2024, 12, 30, 12, 34, 56, 0, time.UTC),
		time.Date(2027, 1, 1, 12, 34, 56, 0, time.UTC),
	} {
		t.Run(date.Format("2006-01-02"), func(t *testing.T) {
			tm := libc.Xmalloc(tls, uint64(unsafe.Sizeof(Ttm{})))
			defer libc.Xfree(tls, tm)
			*(*Ttm)(unsafe.Pointer(tm)) = Ttm{
				Ftm_sec: int32(date.Second()), Ftm_min: int32(date.Minute()), Ftm_hour: int32(date.Hour()),
				Ftm_mday: int32(date.Day()), Ftm_mon: int32(date.Month()) - 1, Ftm_year: int32(date.Year()) - 1900,
				Ftm_wday: int32(date.Weekday()), Ftm_yday: int32(date.YearDay()) - 1,
			}
			format := []rune("%G-W%V-%u %g")
			formatp := libc.Xmalloc(tls, uint64((len(format)+1)*4))
			defer libc.Xfree(tls, formatp)
			for i, r := range format {
				*(*int32)(unsafe.Pointer(formatp + uintptr(i)*4)) = int32(r)
			}
			out := libc.Xmalloc(tls, 64*4)
			defer libc.Xfree(tls, out)
			gotN := _wcsftime(tls, out, 64, formatp, tm)
			isoYear, isoWeek := date.ISOWeek()
			want := fmt.Sprintf("%04d-W%02d-%d %02d", isoYear, isoWeek, (int(date.Weekday())+6)%7+1, isoYear%100)
			got := string(unsafe.Slice((*rune)(unsafe.Pointer(out)), int(gotN)))
			if got != want {
				t.Fatalf("_wcsftime ISO output = %q, want %q", got, want)
			}
		})
	}
}
