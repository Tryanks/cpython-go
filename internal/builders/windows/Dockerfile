# Native linux/arm64 builder for cross-compiling CPython to Windows.
# Build with: docker build --platform linux/arm64 ...
FROM python:3.14.7-slim-bookworm AS build-python

FROM golang:1.27.0-bookworm

ARG LLVM_MINGW_VERSION=20260826
ARG LLVM_MINGW_SHA256=4eb475cccf5e5e37ea3b693a52227e70a86ae70abafceb9ecd83887e67699c9d
ARG LLVM_MINGW_ARCHIVE=llvm-mingw-20260826-ucrt-ubuntu-22.04-aarch64.tar.xz

RUN test "$(dpkg --print-architecture)" = arm64 \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        autoconf \
        autoconf-archive \
        automake \
        ca-certificates \
        curl \
        file \
        git \
        make \
        patch \
        pkg-config \
        xz-utils \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fL --retry 5 \
        "https://github.com/mstorsjo/llvm-mingw/releases/download/${LLVM_MINGW_VERSION}/${LLVM_MINGW_ARCHIVE}" \
        -o /tmp/llvm-mingw.tar.xz \
    && echo "${LLVM_MINGW_SHA256}  /tmp/llvm-mingw.tar.xz" | sha256sum -c - \
    && mkdir -p /opt/llvm-mingw \
    && tar -xJf /tmp/llvm-mingw.tar.xz --strip-components=1 -C /opt/llvm-mingw \
    && rm /tmp/llvm-mingw.tar.xz

COPY --from=build-python /usr/local/ /usr/local/

ENV PATH=/opt/llvm-mingw/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin
ENV GOFLAGS=-mod=mod
WORKDIR /src

RUN python3.14 --version \
    && go version \
    && x86_64-w64-mingw32-clang --version \
    && aarch64-w64-mingw32-clang --version
