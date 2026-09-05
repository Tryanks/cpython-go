// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

// Package stdlib embeds the pure-Python part of the CPython 3.14 standard
// library and materializes it as lib/python314.zip under a per-user cache
// directory, which CPython's getpath accepts as a prefix landmark.
package stdlib

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

// frozenSources are the installed source files corresponding to CPython's
// frozen stdlib modules. FrozenImporter records these paths in __file__, and
// tools such as inspect and linecache expect them to exist on disk.
var frozenSources = map[string]bool{
	"_collections_abc.py":    true,
	"_sitebuiltins.py":       true,
	"abc.py":                 true,
	"codecs.py":              true,
	"genericpath.py":         true,
	"importlib/machinery.py": true,
	"importlib/util.py":      true,
	"io.py":                  true,
	"ntpath.py":              true,
	"os.py":                  true,
	"posixpath.py":           true,
	"runpy.py":               true,
	"site.py":                true,
	"stat.py":                true,
}

// Home returns a directory usable as PYTHONHOME, extracting the embedded
// stdlib on first use. The directory name includes a content hash, so a new
// build never reads a stale extraction.
func Home() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	sum := sha256.Sum256(zipData)
	home := filepath.Join(base, "cpython-go", hex.EncodeToString(sum[:8]))
	zipPath := filepath.Join(home, "lib", "python314.zip")
	if _, err := os.Stat(zipPath); err != nil {
		if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
			return "", err
		}
		if err := writeFileAtomic(zipPath, bytes.NewReader(zipData)); err != nil {
			return "", err
		}
	}
	if err := materializeFrozenSources(home); err != nil {
		return "", err
	}
	return home, nil
}

func materializeFrozenSources(home string) error {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return err
	}
	stdlibDir := filepath.Join(home, "lib", "python3.14")
	for _, f := range zr.File {
		if !frozenSources[f.Name] {
			continue
		}
		dst := filepath.Join(stdlibDir, filepath.FromSlash(f.Name))
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFileAtomic(dst, r)
		closeErr := r.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func writeFileAtomic(dst string, src io.Reader) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Rename is atomic: concurrent first runs cannot observe a partial file.
	if err := os.Rename(name, dst); err != nil {
		// Windows does not replace an existing destination. Another process
		// winning the race is still success.
		if _, statErr := os.Stat(dst); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}
