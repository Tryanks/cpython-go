// Command typecheck type-checks a package with go/types and prints every
// error with its exact position. The gc compiler stops after 10 errors and
// clamps line numbers in files longer than ~1M lines, which makes it useless
// for triaging the generated libpython package.
//
// Usage: go run ./internal/cmd/typecheck ./libpython
package main

import (
	"fmt"
	"os"

	"golang.org/x/tools/go/packages"
)

func main() {
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedName}, os.Args[1:]...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	n := 0
	for _, p := range pkgs {
		for _, e := range p.Errors {
			fmt.Println(e)
			n++
		}
		for _, e := range p.TypeErrors {
			fmt.Printf("%s: %s\n", e.Fset.Position(e.Pos), e.Msg)
			n++
		}
	}
	fmt.Fprintf(os.Stderr, "%d errors\n", n)
	if n != 0 {
		os.Exit(1)
	}
}
