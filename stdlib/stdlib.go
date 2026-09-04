// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

// Package stdlib embeds the pure-Python part of the CPython 3.14 standard
// library and materializes it as lib/python314.zip under a per-user cache
// directory, which CPython's getpath accepts as a prefix landmark.
package stdlib

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

// Home returns a directory usable as PYTHONHOME, extracting the embedded
// stdlib on first use. The directory name includes a content hash, so a new
// build never reads a stale extraction.
func Home() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	sum := sha256.Sum256(zipGz)
	home := filepath.Join(base, "cpython-go", hex.EncodeToString(sum[:8]))
	zipPath := filepath.Join(home, "lib", "python314.zip")
	if _, err := os.Stat(zipPath); err == nil {
		return home, nil
	}

	if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(zipPath), "python314.zip.*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	gz, err := gzip.NewReader(bytes.NewReader(zipGz))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, gz); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	// Rename is atomic: concurrent first runs cannot observe a partial zip.
	if err := os.Rename(tmp.Name(), zipPath); err != nil {
		return "", err
	}
	return home, nil
}
