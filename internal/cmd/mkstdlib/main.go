// Command mkstdlib packs a CPython Lib/ directory into a gzip-compressed,
// *stored* (uncompressed entries: the interpreter has no zlib) zip file that
// package stdlib embeds.
//
// Usage: go run ./internal/cmd/mkstdlib -o stdlib/python314.zip.gz <Lib dir> [extra .py files...]
package main

import (
	"archive/zip"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// skip lists top-level Lib/ entries not worth shipping.
var skip = map[string]bool{
	"__pycache__": true, "ensurepip": true, "idlelib": true, "lib2to3": true,
	"test": true, "tkinter": true, "turtledemo": true, "turtle.py": true,
}

func main() {
	out := flag.String("o", "python314.zip.gz", "output file")
	tests := flag.Bool("tests", false, "include Lib/test (for running CPython's test suite)")
	flag.Parse()
	if *tests {
		delete(skip, "test")
	}
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: mkstdlib -o out.zip.gz <Lib dir> [extra files...]")
		os.Exit(2)
	}

	f, err := os.Create(*out)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	zw := zip.NewWriter(gz)
	add := func(name string, r io.Reader) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			fail(err)
		}
		if _, err := io.Copy(w, r); err != nil {
			fail(err)
		}
	}
	lib := flag.Arg(0)
	n := 0
	err = filepath.WalkDir(lib, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(lib, p)
		if rel == "." {
			return nil
		}
		top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		if skip[top] || d.Name() == "__pycache__" || strings.HasSuffix(rel, "_test") && d.IsDir() {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".py") && !strings.HasSuffix(d.Name(), ".txt") && !strings.HasSuffix(d.Name(), ".json") && !strings.HasSuffix(d.Name(), ".pem") {
			return nil
		}
		r, err := os.Open(p)
		if err != nil {
			return err
		}
		defer r.Close()
		add(filepath.ToSlash(rel), r)
		n++
		return nil
	})
	if err != nil {
		fail(err)
	}
	for _, extra := range flag.Args()[1:] {
		r, err := os.Open(extra)
		if err != nil {
			fail(err)
		}
		add(path.Base(extra), r)
		r.Close()
		n++
	}
	if err := zw.Close(); err != nil {
		fail(err)
	}
	if err := gz.Close(); err != nil {
		fail(err)
	}
	fmt.Printf("%s: %d files\n", *out, n)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
