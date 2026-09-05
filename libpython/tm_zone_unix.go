//go:build darwin || linux

package libpython

import "modernc.org/libc"

func tmZone(tmv *Ttm) (string, int) {
	name := ""
	if tmv.Ftm_zone != 0 {
		name = libc.GoString(tmv.Ftm_zone)
	}
	return name, int(tmv.Ftm_gmtoff)
}
