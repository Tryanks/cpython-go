#!/bin/sh
# Cross-compile the CPython 3.14.7 static library for Windows.
#
#   internal/builders/windows/run.sh amd64 /tmp/cpython-3.14.7
#   internal/builders/windows/run.sh --ccgo amd64 /tmp/cpython-3.14.7
#   WINDOWS_BUILDER_SKIP_BUILD=1 internal/builders/windows/run.sh arm64 /tmp/cpython-3.14.7
set -eu

if [ "${1:-}" = --ccgo ]; then
	shift
	ccgo=1
else
	ccgo=0
fi

if [ "${1:-}" = --inside ]; then
	shift
	inside=1
else
	inside=0
fi

arch=${1:?usage: run.sh <amd64|arm64> <cpython-3.14.7-src>}
src=${2:?usage: run.sh <amd64|arm64> <cpython-3.14.7-src>}

case "$arch" in
	amd64) host=x86_64-w64-mingw32 ;;
	arm64) host=aarch64-w64-mingw32 ;;
	*) echo "unsupported architecture: $arch (expected amd64 or arm64)" >&2; exit 2 ;;
esac

here=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo=$(CDPATH= cd -- "$here/../../.." && pwd)
expected_cpython=823f0323ee6ec1402088b73bce1a38473cac36dc

if [ "$inside" -eq 0 ]; then
	actual_cpython=$(git -C "$src" rev-parse HEAD)
	if [ "$actual_cpython" != "$expected_cpython" ]; then
		echo "CPython source must be exactly v3.14.7 ($expected_cpython), got $actual_cpython" >&2
		exit 2
	fi
	if [ -z "${WINDOWS_BUILDER_SKIP_BUILD:-}" ]; then
		docker build --platform linux/arm64 -t cpython-go-builder:windows "$here"
	fi
	ccgo_arg=
	if [ "$ccgo" -eq 1 ]; then
		ccgo_arg=--ccgo
	fi
	exec docker run --rm --platform linux/arm64 \
		-v "$repo":/src \
		-v "$src":/cpython:ro \
		-v cpython-go-gomod-windows:/go/pkg/mod \
		-e JOBS="${JOBS:-4}" \
		cpython-go-builder:windows \
		/src/internal/builders/windows/run.sh $ccgo_arg --inside "$arch" /cpython
fi

if [ "$ccgo" -eq 1 ]; then
	export CPYTHON_SRC="$src"
	export TARGET_GOOS=windows
	export TARGET_GOARCH="$arch"
	export MINGW_TRIPLE="$host"
	export BUILD_TRIPLE=aarch64-unknown-linux-gnu
	export BUILD_PYTHON=/usr/local/bin/python3.14
	export CONFIG_SITE="/src/internal/builders/windows/config.site.${arch}"
	export GOMAXPROCS="${JOBS:-4}"
	exec go run generator.go
fi

scratch="/src/tmp/windows_${arch}"
cpython="$scratch/cpython"
build="$scratch/build"
patch_log="$scratch/cpython-3.14.diff.log"

rm -rf "$cpython" "$build"
mkdir -p "$cpython" "$build"
cp -a "$src/." "$cpython/"

echo "Applying ccgo preparation patch opportunistically (rejections are recorded):"
set +e
patch --batch --forward --reject-file=- -p1 -d "$cpython" \
	< /src/internal/patch/cpython-3.14.diff > "$patch_log" 2>&1
ccgo_patch_status=$?
set -e
cat "$patch_log"
if [ "$ccgo_patch_status" -ne 0 ]; then
	echo "NOTE: internal/patch/cpython-3.14.diff had rejected hunks; continuing for the plain C cross-build."
fi

echo "Applying the MSYS2 CPython 3.14.7 MinGW patch set:"
patch --batch --forward -p1 -d "$cpython" \
	< /src/internal/patch/windows/cpython-3.14.7-msys2.diff

(
	cd "$cpython"
	autoreconf -vfi
)

export CONFIG_SITE="/src/internal/builders/windows/config.site.${arch}"
export MODULE_BUILDTYPE=static
export PKG_CONFIG_LIBDIR=/nonexistent
export PKG_CONFIG_PATH=
export CC="${host}-clang"
export CXX="${host}-clang++"
export AR="${host}-ar"
export RANLIB="${host}-ranlib"
export READELF="${host}-readelf"
export STRIP="${host}-strip"
export WINDRES="${host}-windres"

(
	cd "$build"
	"$cpython/configure" \
		--host="$host" \
		--build=aarch64-unknown-linux-gnu \
		--prefix=/usr/local \
		--with-build-python=/usr/local/bin/python3.14 \
		--enable-ipv6 \
		--disable-shared \
		--disable-test-modules \
		--disable-experimental-jit \
		--with-static-libpython \
		--with-system-libmpdec=no \
		--without-computed-gotos \
		--without-ensurepip \
		--without-mimalloc \
		--without-remote-debug \
		--without-pymalloc
)

make -C "$build" -j "${JOBS:-4}" \
	GITVERSION=: GITTAG=: GITBRANCH=: \
	libpython3.14.a

test -s "$build/libpython3.14.a"
echo "Built $build/libpython3.14.a"
"${host}-ar" t "$build/libpython3.14.a" | wc -l
