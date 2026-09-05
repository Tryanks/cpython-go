//go:build cpython_test

// Built with -tags cpython_test, the binary embeds the stdlib including
// Lib/test (make stdlib-tests), so subprocesses spawned by the test suite
// with -I/-E can import the test package.

package stdlib

import _ "embed"

//go:embed python314_tests.zip
var zipData []byte
