// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

// Command cpython-go is a pure Go build of the CPython interpreter. It
// accepts the same command line as python3. The standard library is embedded
// and extracted to the user cache directory on first run unless PYTHONHOME is
// set.
package main

import (
	"fmt"
	"os"
	"runtime/pprof"

	cpython "github.com/Tryanks/cpython-go"
)

func main() {
	os.Exit(run())
}

func run() int {
	profile := os.Getenv("CPYTHON_GO_CPUPROFILE")
	if profile == "" {
		return cpython.Main(os.Args)
	}
	f, err := os.Create(profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cpython-go: create CPU profile:", err)
		return 2
	}
	defer f.Close()
	if err := pprof.StartCPUProfile(f); err != nil {
		fmt.Fprintln(os.Stderr, "cpython-go: start CPU profile:", err)
		return 2
	}
	defer pprof.StopCPUProfile()
	return cpython.Main(os.Args)
}
