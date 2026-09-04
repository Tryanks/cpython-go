// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

// Command cpython-go is a pure Go build of the CPython interpreter. It
// accepts the same command line as python3. The standard library is embedded
// and extracted to the user cache directory on first run unless PYTHONHOME is
// set.
package main

import (
	"os"

	cpython "github.com/Tryanks/cpython-go"
)

func main() {
	os.Exit(cpython.Main(os.Args))
}
