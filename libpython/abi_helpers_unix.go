//go:build darwin || linux

package libpython

import "modernc.org/libc"

func PyBoolFromBool(tls *libc.TLS, value bool) uintptr {
	if value {
		return XPyBool_FromLong(tls, 1)
	}
	return XPyBool_FromLong(tls, 0)
}
