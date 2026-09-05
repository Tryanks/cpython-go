# Copyright 2026 The cpython-go Authors. All rights reserved.
# Use of this source code is governed by the MIT license that can be found in
# the LICENSE file.

.PHONY: all build test generate regenerate postprocess pgo stdlib clean

CPYTHON_SRC ?= /tmp/cpython-3.14
PPROF ?= go run github.com/google/pprof@v0.0.0-20260709232956-b9395ee17fa0

all: build test

build:
	go build -o tmp/cpython-go ./cmd/cpython-go

test:
	go vet . ./cmd/... ./stdlib ./internal/...
	go test -count=1 . ./stdlib ./internal/...
	go test -count=1 -vet=off ./libpython

# Full generation: copy+patch sources, configure, make under ccgo, link, shard.
generate:
	CPYTHON_SRC=$(CPYTHON_SRC) go run generator.go

# Re-run make (only changed C files), link and shard.
regenerate:
	GO_GENERATE_INCREMENTAL=1 go run generator.go

# Only the rewrites + sharding of an already linked single file.
postprocess:
	GO_GENERATE_POSTPROCESS=1 go run generator.go

# Refresh the Go PGO profile with three runs of the interpreter benchmark.
# The training binary explicitly disables the existing profile.
pgo:
	mkdir -p tmp/pgo
	go build -pgo=off -o tmp/cpython-go-pgo-training ./cmd/cpython-go
	for n in 1 2 3; do \
		CPYTHON_GO_CPUPROFILE=tmp/pgo/profile-$$n.pprof \
			./tmp/cpython-go-pgo-training internal/bench/bench.py; \
	done
	$(PPROF) -proto -output=cmd/cpython-go/default.pgo \
		tmp/pgo/profile-1.pprof tmp/pgo/profile-2.pprof tmp/pgo/profile-3.pprof

stdlib:
	go run ./internal/cmd/mkstdlib -o stdlib/python314.zip tmp/darwin_arm64/cpython/Lib internal/stdlib-extra/*.py stdlib/sysconfigdata/*.py

# Same, plus Lib/test, for `go build -tags cpython_test` (see stdlib/embed_tests.go).
stdlib-tests:
	go run ./internal/cmd/mkstdlib -tests -o stdlib/python314_tests.zip tmp/darwin_arm64/cpython/Lib internal/stdlib-extra/*.py stdlib/sysconfigdata/*.py

clean:
	rm -rf tmp/*_*/build tmp/cpython-go
