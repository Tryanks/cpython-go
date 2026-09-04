#!/bin/sh
# Generate libpython for one Linux architecture inside a container.
#
#   internal/builders/linux/run.sh arm64 /tmp/cpython-3.14
#
# Non-native architectures run under qemu-user (OrbStack/Docker Desktop
# provide binfmt); expect 1-3 hours for those. The repo is mounted read-write:
# the output shards land in libpython/ and scratch in tmp/linux_<arch>/.
set -eu
arch=${1:?arch (386 amd64 arm arm64 loong64 ppc64le riscv64 s390x)}
src=${2:?path to CPython 3.14 sources}
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)
case $arch in
	386) platform=linux/386 ;;
	arm) platform=linux/arm/v7 ;;
	*) platform=linux/$arch ;;
esac
docker build --platform "$platform" -t cpython-go-builder:"$arch" "$here"
docker run --rm --platform "$platform" \
	-v "$repo":/src -v "$src":/cpython:ro \
	-v cpython-go-gomod-"$arch":/go/pkg/mod \
	-e CPYTHON_SRC=/cpython -e GOARCH="$arch" \
	cpython-go-builder:"$arch" \
	go run generator.go
