package cpython_test

import (
	"os"
	"testing"

	"github.com/Tryanks/cpython-go"
	"github.com/Tryanks/cpython-go/stdlib"
)

func TestDiagnosticNew(t *testing.T) {
	home, err := stdlib.Home()
	t.Logf("home=%q err=%v executable=%q argv=%q", home, err, os.Args[0], os.Args)
	opts := []cpython.Option{}
	if os.Getenv("CPGO_DIAG_PYTHON_CONFIG") != "" {
		opts = append(opts, cpython.WithIsolated(false))
	}
	in, err := cpython.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
}
