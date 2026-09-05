// Command undup folds byte-identical declarations from splitgo's per-target
// shards into build-tagged shared shards. It can also expand that layout back
// to complete per-target shards before a target is regenerated.
package main

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	base          = "ccgo"
	shards        = 12
	maxShardBytes = 8_000_000
)

var targetNames = []string{
	"darwin_amd64",
	"darwin_arm64",
	"linux_amd64",
	"linux_arm64",
	"windows_amd64",
	"windows_arm64",
}

type key struct {
	hash       [32]byte
	occurrence uint32
}

type declaration struct {
	key  key
	name string
	src  []byte
}

type target struct {
	name     string
	buildTag string
	decls    []declaration
}

type output struct {
	path string
	data []byte
}

func main() {
	dir := flag.String("dir", "libpython", "directory containing ccgo generated files")
	expand := flag.Bool("expand", false, "reconstruct complete per-target shards and remove shared shards")
	flag.Parse()
	var err error
	if *expand {
		err = expandTree(*dir)
	} else {
		err = dedupTree(*dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "undup:", err)
		os.Exit(1)
	}
}

func dedupTree(dir string) error {
	if shared, _ := filepath.Glob(filepath.Join(dir, base+"_g_*.go")); len(shared) != 0 {
		fmt.Println("undup: tree is already deduplicated")
		return nil
	}

	targets := make([]target, len(targetNames))
	var header []byte
	var inBytes int64
	for i, name := range targetNames {
		t := target{name: name, buildTag: strings.Replace(name, "_", " && ", 1)}
		counts := map[[32]byte]uint32{}
		for shard := 0; shard < shards; shard++ {
			path := filepath.Join(dir, fmt.Sprintf("%s_%s_%02d.go", base, name, shard))
			decls, fileHeader, size, err := loadFile(path, counts)
			if err != nil {
				return err
			}
			if len(header) == 0 {
				header = fileHeader
			} else if !bytes.Equal(header, fileHeader) {
				return fmt.Errorf("%s: import/guard header differs from the first shard", path)
			}
			t.decls = append(t.decls, decls...)
			inBytes += size
		}
		targets[i] = t
	}

	sets := map[key]uint64{}
	body := map[key]declaration{}
	for i := range targets {
		for _, d := range targets[i].decls {
			sets[d.key] |= uint64(1) << uint(i)
			if _, ok := body[d.key]; !ok {
				body[d.key] = d
			}
		}
	}

	byMask := map[uint64][]declaration{}
	for k, mask := range sets {
		d := body[k]
		byMask[mask] = append(byMask[mask], d)
	}
	for mask := range byMask {
		sortDecls(byMask[mask])
	}

	var outputs []output
	var sharedBytes int64
	var sharedDecls int
	var masks []uint64
	for mask := range byMask {
		if bits(mask) >= 2 {
			masks = append(masks, mask)
		}
	}
	sort.Slice(masks, func(i, j int) bool {
		if a, b := bits(masks[i]), bits(masks[j]); a != b {
			return a > b
		}
		return masks[i] < masks[j]
	})
	for _, mask := range masks {
		groups := splitBySize(byMask[mask], maxShardBytes-len(header)-512)
		for i, group := range groups {
			name := fmt.Sprintf("%s_g_%016x_%02d.go", base, mask, i)
			data := render(combinedTag(mask), header, group)
			outputs = append(outputs, output{filepath.Join(dir, name), data})
			sharedBytes += int64(len(data))
			sharedDecls += len(group)
		}
	}

	for i, t := range targets {
		unique := byMask[uint64(1)<<uint(i)]
		groups := splitByCount(unique, shards)
		for shard, group := range groups {
			path := filepath.Join(dir, fmt.Sprintf("%s_%s_%02d.go", base, t.name, shard))
			outputs = append(outputs, output{path, render(t.buildTag, header, group)})
		}
	}

	if err := verify(targets, outputs); err != nil {
		return err
	}
	if err := writeOutputs(outputs); err != nil {
		return err
	}
	var outBytes int64
	for _, o := range outputs {
		outBytes += int64(len(o.data))
	}
	fmt.Printf("undup: %d targets, %d shared declarations in %d shared shards; %.1f MB -> %.1f MB (shared %.1f MB)\n",
		len(targets), sharedDecls, len(outputs)-len(targets)*shards,
		float64(inBytes)/1e6, float64(outBytes)/1e6, float64(sharedBytes)/1e6)
	return nil
}

func expandTree(dir string) error {
	sharedPaths, err := filepath.Glob(filepath.Join(dir, base+"_g_*.go"))
	if err != nil {
		return err
	}
	if len(sharedPaths) == 0 {
		fmt.Println("undup: tree is already expanded")
		return nil
	}
	sort.Strings(sharedPaths)

	type sharedFile struct {
		mask  uint64
		decls []declaration
	}
	var shared []sharedFile
	var header []byte
	for _, path := range sharedPaths {
		mask, err := maskFromShared(filepath.Base(path))
		if err != nil {
			return err
		}
		decls, h, _, err := loadFile(path, map[[32]byte]uint32{})
		if err != nil {
			return err
		}
		if len(header) == 0 {
			header = h
		}
		shared = append(shared, sharedFile{mask, decls})
	}

	var outputs []output
	for i, name := range targetNames {
		counts := map[[32]byte]uint32{}
		var decls []declaration
		for shard := 0; shard < shards; shard++ {
			path := filepath.Join(dir, fmt.Sprintf("%s_%s_%02d.go", base, name, shard))
			ds, h, _, err := loadFile(path, counts)
			if err != nil {
				return err
			}
			if len(header) == 0 {
				header = h
			}
			decls = append(decls, ds...)
		}
		// Shared declarations were keyed independently in their output files;
		// rebuild occurrence keys after gathering the target's complete set.
		var gathered []declaration
		gathered = append(gathered, decls...)
		for _, sf := range shared {
			if sf.mask&(uint64(1)<<uint(i)) != 0 {
				gathered = append(gathered, sf.decls...)
			}
		}
		sortDecls(gathered)
		groups := splitByCount(gathered, shards)
		tag := strings.Replace(name, "_", " && ", 1)
		for shard, group := range groups {
			path := filepath.Join(dir, fmt.Sprintf("%s_%s_%02d.go", base, name, shard))
			outputs = append(outputs, output{path, render(tag, header, group)})
		}
	}
	if err := writeOutputs(outputs); err != nil {
		return err
	}
	for _, path := range sharedPaths {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	fmt.Printf("undup: expanded %d shared shards into %d per-target shards\n", len(sharedPaths), len(outputs))
	return nil
}

func loadFile(path string, counts map[[32]byte]uint32) ([]declaration, []byte, int64, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, 0, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, nil, 0, err
	}
	var real []ast.Decl
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && (gd.Tok == token.IMPORT || isGuard(gd)) {
			continue
		}
		real = append(real, d)
	}
	headerStart := packageEnd(src, fset.Position(f.Name.End()).Offset)
	if len(real) == 0 {
		return nil, append([]byte(nil), src[headerStart:]...), int64(len(src)), nil
	}
	headerEnd := fset.Position(declStart(real[0])).Offset
	header := append([]byte(nil), src[headerStart:headerEnd]...)

	decls := make([]declaration, 0, len(real))
	for i, d := range real {
		start := fset.Position(declStart(d)).Offset
		end := len(src)
		if i+1 < len(real) {
			end = fset.Position(declStart(real[i+1])).Offset
		}
		text := append([]byte(nil), bytes.TrimRight(src[start:end], " \t\r\n")...)
		text = append(text, '\n')
		h := sha256.Sum256(text)
		k := key{h, counts[h]}
		counts[h]++
		decls = append(decls, declaration{k, declName(d), text})
	}
	return decls, header, int64(len(src)), nil
}

func packageEnd(src []byte, nameEnd int) int {
	i := nameEnd
	for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n') {
		i++
	}
	return i
}

func declStart(d ast.Decl) token.Pos {
	switch x := d.(type) {
	case *ast.FuncDecl:
		if x.Doc != nil {
			return x.Doc.Pos()
		}
	case *ast.GenDecl:
		if x.Doc != nil {
			return x.Doc.Pos()
		}
	}
	return d.Pos()
}

func declName(d ast.Decl) string {
	switch x := d.(type) {
	case *ast.FuncDecl:
		return x.Name.Name
	case *ast.GenDecl:
		if len(x.Specs) != 0 {
			switch s := x.Specs[0].(type) {
			case *ast.TypeSpec:
				return s.Name.Name
			case *ast.ValueSpec:
				if len(s.Names) != 0 {
					return s.Names[0].Name
				}
			}
		}
	}
	return ""
}

func isGuard(gd *ast.GenDecl) bool {
	if gd.Tok != token.VAR {
		return false
	}
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			return false
		}
		for _, n := range vs.Names {
			if n.Name != "_" {
				return false
			}
		}
	}
	return true
}

func render(buildTag string, header []byte, decls []declaration) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "//go:build %s\n\n", buildTag)
	out.WriteString("// Code generated by undup; DO NOT EDIT.\n\npackage libpython\n\n")
	out.Write(ensureLibcGuard(header))
	for _, d := range decls {
		out.Write(d.src)
		out.WriteByte('\n')
	}
	return bytes.TrimRight(out.Bytes(), " \t\r\n")
}

func ensureLibcGuard(header []byte) []byte {
	if bytes.Contains(header, []byte("var _ *libc.TLS")) {
		return header
	}
	guard := []byte("var _ *libc.TLS\n\n")
	return append(append([]byte(nil), header...), guard...)
}

func combinedTag(mask uint64) string {
	parts := make([]string, 0, len(targetNames))
	for i, name := range targetNames {
		if mask&(uint64(1)<<uint(i)) != 0 {
			parts = append(parts, "("+strings.Replace(name, "_", " && ", 1)+")")
		}
	}
	return strings.Join(parts, " || ")
}

func splitBySize(decls []declaration, limit int) [][]declaration {
	if len(decls) == 0 {
		return nil
	}
	var groups [][]declaration
	for _, d := range decls {
		if len(groups) == 0 || groupBytes(groups[len(groups)-1])+len(d.src) > limit {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], d)
	}
	return groups
}

func groupBytes(group []declaration) int {
	n := 0
	for _, d := range group {
		n += len(d.src) + 1
	}
	return n
}

func splitByCount(decls []declaration, count int) [][]declaration {
	groups := make([][]declaration, count)
	total := groupBytes(decls)
	index, consumed := 0, 0
	for _, d := range decls {
		for index < count-1 && consumed >= total*(index+1)/count {
			index++
		}
		groups[index] = append(groups[index], d)
		consumed += len(d.src) + 1
	}
	return groups
}

func sortDecls(decls []declaration) {
	sort.Slice(decls, func(i, j int) bool {
		if decls[i].name != decls[j].name {
			return decls[i].name < decls[j].name
		}
		if c := bytes.Compare(decls[i].key.hash[:], decls[j].key.hash[:]); c != 0 {
			return c < 0
		}
		return decls[i].key.occurrence < decls[j].key.occurrence
	})
}

func bits(mask uint64) int {
	n := 0
	for mask != 0 {
		mask &= mask - 1
		n++
	}
	return n
}

func maskFromShared(name string) (uint64, error) {
	prefix := base + "_g_"
	if !strings.HasPrefix(name, prefix) {
		return 0, fmt.Errorf("invalid shared shard name %q", name)
	}
	rest := strings.TrimPrefix(name, prefix)
	hex, _, ok := strings.Cut(rest, "_")
	if !ok {
		return 0, fmt.Errorf("invalid shared shard name %q", name)
	}
	mask, err := strconv.ParseUint(hex, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid shared shard name %q: %w", name, err)
	}
	return mask, nil
}

func verify(targets []target, outputs []output) error {
	// The plan is derived directly from each declaration's target mask. Check
	// every original occurrence is selected exactly once for its target.
	selected := make([]map[key]int, len(targets))
	for i := range selected {
		selected[i] = map[key]int{}
	}
	for _, o := range outputs {
		name := filepath.Base(o.path)
		var mask uint64
		if strings.HasPrefix(name, base+"_g_") {
			var err error
			mask, err = maskFromShared(name)
			if err != nil {
				return err
			}
		} else {
			for i, targetName := range targetNames {
				if strings.HasPrefix(name, base+"_"+targetName+"_") {
					mask = uint64(1) << uint(i)
					break
				}
			}
		}
		counts := map[[32]byte]uint32{}
		decls, _, _, err := loadBytes(name, o.data, counts)
		if err != nil {
			return err
		}
		for i := range targets {
			if mask&(uint64(1)<<uint(i)) != 0 {
				for _, d := range decls {
					selected[i][d.key]++
				}
			}
		}
	}
	for i, t := range targets {
		want := map[key]int{}
		for _, d := range t.decls {
			want[d.key]++
		}
		if len(want) != len(selected[i]) {
			return fmt.Errorf("verification failed for %s: %d declarations became %d", t.name, len(want), len(selected[i]))
		}
		for k, n := range want {
			if selected[i][k] != n {
				return fmt.Errorf("verification failed for %s: declaration occurrence mismatch", t.name)
			}
		}
	}
	return nil
}

func loadBytes(name string, src []byte, counts map[[32]byte]uint32) ([]declaration, []byte, int64, error) {
	tmp, err := os.CreateTemp("", "undup-verify-*.go")
	if err != nil {
		return nil, nil, 0, err
	}
	path := tmp.Name()
	if _, err = tmp.Write(src); err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	defer os.Remove(path)
	if err != nil {
		return nil, nil, 0, err
	}
	return loadFile(path, counts)
}

func writeOutputs(outputs []output) error {
	for _, o := range outputs {
		data := append(bytes.TrimRight(o.data, " \t\r\n"), '\n')
		if err := os.WriteFile(o.path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
