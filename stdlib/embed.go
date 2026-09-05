//go:build !cpython_test

package stdlib

import _ "embed"

//go:embed python314.zip
var zipData []byte
