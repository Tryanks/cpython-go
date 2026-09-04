// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

//go:generate go run ../generator.go

// Package libpython is the ccgo transpilation of CPython 3.12.
package libpython // import "github.com/tryanks/cpython-go/libpython"

import (
	_ "modernc.org/libc"
)
