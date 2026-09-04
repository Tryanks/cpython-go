# Builder image for generating libpython/ccgo_linux_<arch>.go.
#
# Alpine (musl) so that CPython's configure sees the same libc headers that
# ccgo compiles against (modernc.org/libc on linux is transpiled musl).
FROM golang:1.27-alpine
RUN apk add --no-cache build-base make git patch
ENV GOFLAGS=-mod=mod
WORKDIR /src
