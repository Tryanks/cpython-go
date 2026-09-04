package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitFile(t *testing.T) {
	const input = `// generated input
//go:build darwin && arm64

package sample

import (
	"math"
	"reflect"
	"unsafe"
)

var _ = math.Pi
var _ reflect.Type
var _ unsafe.Pointer

type octet = byte
type chained = octet
type word = uint16
const marker = 0x41

// bytesComment stays with bytesTable.
var bytesTable = [64]chained{'a', uint8(2), chained(0x3), +4}

var words = [32]word{0: word(0x1234), 1: marker}
var signedBytes = [64]int8{-1, 'A'}

var structs = [20]struct{ X int }{{X: 1}}

func Alpha() int { return 1 }

// BetaComment stays with Beta.
func Beta() int { return 2 }

var expression = [64]byte{1 + 2}
`
	dir := t.TempDir()
	in := filepath.Join(dir, "in.go")
	if err := os.WriteFile(in, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := splitFile(in, options{outputDir: out, base: "sample", shards: 3}); err != nil {
		t.Fatal(err)
	}

	blob, err := os.ReadFile(filepath.Join(out, "sample_data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) != 192 {
		t.Fatalf("blob length = %d, want 192", len(blob))
	}
	wantPrefix := []byte{'a', 2, 3, 4, 0, 0}
	if !bytes.Equal(blob[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("byte table prefix = %v, want %v", blob[:len(wantPrefix)], wantPrefix)
	}
	if got := blob[64:70]; !bytes.Equal(got, []byte{0x34, 0x12, 0x41, 0, 0, 0}) {
		t.Fatalf("word table prefix = %v", got)
	}
	if got := blob[128:132]; !bytes.Equal(got, []byte{0xff, 0x41, 0, 0}) {
		t.Fatalf("signed byte table prefix = %v", got)
	}

	dataGo := readTestFile(t, filepath.Join(out, "sample_data.go"))
	for _, want := range []string{"//go:build darwin && arm64", "// bytesComment stays with bytesTable.", "var bytesTable [64]chained", "var words [32]word", "var signedBytes [64]int8", "copy(bytesTable[:]", "unsafe.Pointer(&signedBytes)", "unsafe.Slice"} {
		if !strings.Contains(dataGo, want) {
			t.Errorf("data file does not contain %q", want)
		}
	}

	var combined strings.Builder
	for i := 0; i < 3; i++ {
		name := filepath.Join(out, "sample_0"+string(rune('0'+i))+".go")
		shard := readTestFile(t, name)
		combined.WriteString(shard)
		for _, want := range []string{"//go:build darwin && arm64", `"math"`, "var _ = math.Pi"} {
			if !strings.Contains(shard, want) {
				t.Errorf("%s does not contain %q", name, want)
			}
		}
	}
	all := combined.String()
	if strings.Contains(all, "var bytesTable") || strings.Contains(all, "var words") {
		t.Error("moved declarations remain in shards")
	}
	for _, want := range []string{"var structs", "var expression", "// BetaComment stays with Beta."} {
		if !strings.Contains(all, want) {
			t.Errorf("shards do not contain %q", want)
		}
	}
	if strings.Index(all, "func Alpha") > strings.Index(all, "func Beta") {
		t.Error("declaration order changed across shards")
	}
}

func TestShardDeclarationsUsesBoundaries(t *testing.T) {
	decls := [][]byte{[]byte("aaa"), []byte("bbbb"), []byte("cc"), []byte("ddddd")}
	groups := shardDeclarations(decls, 3)
	var got []string
	for _, group := range groups {
		for _, decl := range group {
			got = append(got, string(decl))
		}
	}
	if strings.Join(got, ",") != "aaa,bbbb,cc,ddddd" {
		t.Fatalf("declaration order/boundaries changed: %v", got)
	}
	if len(groups) != 3 || len(groups[0]) == 0 || len(groups[1]) == 0 || len(groups[2]) == 0 {
		t.Fatalf("unexpected shard allocation: %#v", groups)
	}
}

func readTestFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
