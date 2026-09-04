// Command splitgo moves large integer array initializers into an embedded binary
// blob and divides the remaining declarations among smaller Go source files.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const minimumDataBytes = 64

type integerType struct {
	width  int
	signed bool
}

type movedVar struct {
	name     string
	typeText string
	docText  string
	width    int
	signed   bool
	offset   int
	size     int
	data     []byte
}

type skippedVar struct {
	name        string
	sourceBytes int
	reason      string
}

type options struct {
	outputDir string
	base      string
	shards    int
}

func main() {
	var opts options
	flag.StringVar(&opts.outputDir, "o", "", "output directory")
	flag.StringVar(&opts.base, "base", "", "output file base name")
	flag.IntVar(&opts.shards, "shards", 0, "number of source shards")
	flag.Parse()
	if opts.outputDir == "" || opts.base == "" || opts.shards < 1 || flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: splitgo -o OUTDIR -base NAME -shards N INPUT.go")
		os.Exit(2)
	}
	if filepath.Base(opts.base) != opts.base || strings.ContainsAny(opts.base, `/\\`) {
		fmt.Fprintln(os.Stderr, "splitgo: -base must be a file base name")
		os.Exit(2)
	}
	if err := splitFile(flag.Arg(0), opts); err != nil {
		fmt.Fprintf(os.Stderr, "splitgo: %v\n", err)
		os.Exit(1)
	}
}

func splitFile(input string, opts options) error {
	src, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, input, src, parser.ParseComments)
	if err != nil {
		return err
	}
	buildLine, err := findBuildLine(src)
	if err != nil {
		return err
	}
	aliases := collectAliases(file)
	constants := collectConstants(file, aliases)

	var imports, guards, remaining []ast.Decl
	var moved []movedVar
	var skipped []skippedVar
	var blob []byte
	seenNonHeader := false
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			imports = append(imports, decl)
			continue
		}
		if !seenNonHeader && isImportGuard(decl) {
			guards = append(guards, decl)
			continue
		}
		seenNonHeader = true
		mv, candidate, reason := extractArray(decl, src, fset, aliases, constants)
		if mv != nil {
			mv.offset = len(blob)
			blob = append(blob, mv.data...)
			moved = append(moved, *mv)
			continue
		}
		if candidate != nil {
			candidate.reason = reason
			skipped = append(skipped, *candidate)
		}
		remaining = append(remaining, decl)
	}

	rendered := make([][]byte, len(remaining))
	for i, decl := range remaining {
		rendered[i], err = renderDecl(fset, decl)
		if err != nil {
			return fmt.Errorf("render declaration %d: %w", i, err)
		}
	}
	headerDecls := append(append([]ast.Decl(nil), imports...), guards...)
	header, err := renderDecls(fset, headerDecls)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(opts.outputDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(opts.outputDir, opts.base+"_data.bin"), blob, 0o644); err != nil {
		return err
	}
	dataGo, err := renderDataFile(buildLine, file.Name.Name, opts.base, moved)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(opts.outputDir, opts.base+"_data.go"), dataGo, 0o644); err != nil {
		return err
	}

	groups := shardDeclarations(rendered, opts.shards)
	goBytes := len(dataGo)
	for i, group := range groups {
		var out bytes.Buffer
		writePreamble(&out, buildLine, file.Name.Name)
		out.Write(header)
		for _, decl := range group {
			out.Write(decl)
		}
		formatted, formatErr := format.Source(out.Bytes())
		if formatErr != nil {
			return fmt.Errorf("format shard %d: %w", i, formatErr)
		}
		name := filepath.Join(opts.outputDir, fmt.Sprintf("%s_%02d.go", opts.base, i))
		if err := os.WriteFile(name, formatted, 0o644); err != nil {
			return err
		}
		goBytes += len(formatted)
	}

	fmt.Printf("moved %d variables (%d bytes)\n", len(moved), len(blob))
	fmt.Printf("Go source bytes: %d before, %d after\n", len(src), goBytes)
	if len(skipped) != 0 {
		sort.Slice(skipped, func(i, j int) bool { return skipped[i].sourceBytes > skipped[j].sourceBytes })
		limit := len(skipped)
		if limit > 10 {
			limit = 10
		}
		fmt.Println("largest array initializers left in shards (source bytes):")
		for _, item := range skipped[:limit] {
			fmt.Printf("  %s: %d: %s\n", item.name, item.sourceBytes, item.reason)
		}
	}
	return nil
}

func findBuildLine(src []byte) (string, error) {
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build ") {
			return line, nil
		}
	}
	return "", errors.New("input has no //go:build line")
}

func collectAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts := spec.(*ast.TypeSpec)
			id, ok := ts.Type.(*ast.Ident)
			if ok && ts.Assign.IsValid() {
				aliases[ts.Name.Name] = id.Name
			}
		}
	}
	return aliases
}

type constantResolver struct {
	exprs    map[string]ast.Expr
	aliases  map[string]string
	cache    map[string]constant.Value
	visiting map[string]bool
}

func collectConstants(file *ast.File, aliases map[string]string) *constantResolver {
	r := &constantResolver{
		exprs: make(map[string]ast.Expr), aliases: aliases,
		cache: make(map[string]constant.Value), visiting: make(map[string]bool),
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		var inherited []ast.Expr
		for _, raw := range gen.Specs {
			spec := raw.(*ast.ValueSpec)
			if len(spec.Values) != 0 {
				inherited = spec.Values
			}
			if len(inherited) != len(spec.Names) {
				continue
			}
			for i, name := range spec.Names {
				r.exprs[name.Name] = inherited[i]
			}
		}
	}
	return r
}

func (r *constantResolver) named(name string) (constant.Value, bool) {
	if value, ok := r.cache[name]; ok {
		return value, true
	}
	expr, ok := r.exprs[name]
	if !ok || r.visiting[name] {
		return nil, false
	}
	r.visiting[name] = true
	value, ok := r.integer(expr)
	delete(r.visiting, name)
	if ok {
		r.cache[name] = value
	}
	return value, ok
}

func (r *constantResolver) integer(expr ast.Expr) (constant.Value, bool) {
	switch x := expr.(type) {
	case *ast.BasicLit:
		if x.Kind != token.INT && x.Kind != token.CHAR {
			return nil, false
		}
		v := constant.MakeFromLiteral(x.Value, x.Kind, 0)
		return v, v.Kind() == constant.Int
	case *ast.Ident:
		return r.named(x.Name)
	case *ast.ParenExpr:
		return r.integer(x.X)
	case *ast.UnaryExpr:
		if x.Op != token.ADD && x.Op != token.SUB && x.Op != token.XOR {
			return nil, false
		}
		v, ok := r.integer(x.X)
		if !ok {
			return nil, false
		}
		return constant.UnaryOp(x.Op, v, 0), true
	case *ast.BinaryExpr:
		left, ok := r.integer(x.X)
		if !ok {
			return nil, false
		}
		right, ok := r.integer(x.Y)
		if !ok {
			return nil, false
		}
		if x.Op == token.SHL || x.Op == token.SHR {
			shift, ok := constant.Uint64Val(right)
			if !ok || shift > 1024 {
				return nil, false
			}
			return constant.Shift(left, x.Op, uint(shift)), true
		}
		switch x.Op {
		case token.ADD, token.SUB, token.MUL, token.QUO, token.REM, token.AND, token.OR, token.XOR, token.AND_NOT:
			return constant.BinaryOp(left, x.Op, right), true
		default:
			return nil, false
		}
	case *ast.CallExpr:
		id, ok := x.Fun.(*ast.Ident)
		if !ok || len(x.Args) != 1 || x.Ellipsis.IsValid() {
			return nil, false
		}
		if _, ok := resolveInteger(id.Name, r.aliases); !ok {
			return nil, false
		}
		return r.integer(x.Args[0])
	default:
		return nil, false
	}
}

// elementInteger deliberately accepts only literal-shaped elements (plus a
// reference to a file-local integer constant). General expressions remain in a
// shard even if they happen to be constant-foldable by Go.
func (r *constantResolver) elementInteger(expr ast.Expr) (constant.Value, bool) {
	switch x := expr.(type) {
	case *ast.BasicLit:
		return r.integer(x)
	case *ast.Ident:
		return r.named(x.Name)
	case *ast.ParenExpr:
		return r.elementInteger(x.X)
	case *ast.UnaryExpr:
		if x.Op != token.ADD && x.Op != token.SUB && x.Op != token.XOR {
			return nil, false
		}
		value, ok := r.elementInteger(x.X)
		if !ok {
			return nil, false
		}
		return constant.UnaryOp(x.Op, value, 0), true
	case *ast.CallExpr:
		id, ok := x.Fun.(*ast.Ident)
		if !ok || len(x.Args) != 1 || x.Ellipsis.IsValid() {
			return nil, false
		}
		if _, ok := resolveInteger(id.Name, r.aliases); !ok {
			return nil, false
		}
		return r.elementInteger(x.Args[0])
	default:
		return nil, false
	}
}

func resolveInteger(name string, aliases map[string]string) (integerType, bool) {
	seen := make(map[string]bool)
	for aliases[name] != "" {
		if seen[name] {
			return integerType{}, false
		}
		seen[name] = true
		name = aliases[name]
	}
	types := map[string]integerType{
		"byte": {1, false}, "uint8": {1, false}, "int8": {1, true},
		"uint16": {2, false}, "int16": {2, true}, "uint32": {4, false},
		"int32": {4, true}, "uint64": {8, false}, "int64": {8, true},
	}
	t, ok := types[name]
	return t, ok
}

func extractArray(decl ast.Decl, src []byte, fset *token.FileSet, aliases map[string]string, constants *constantResolver) (*movedVar, *skippedVar, string) {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR || len(gen.Specs) != 1 {
		return nil, nil, ""
	}
	vs := gen.Specs[0].(*ast.ValueSpec)
	if len(vs.Names) != 1 || len(vs.Values) != 1 {
		return nil, nil, ""
	}
	lit, ok := vs.Values[0].(*ast.CompositeLit)
	if !ok {
		return nil, nil, ""
	}
	array, ok := lit.Type.(*ast.ArrayType)
	if !ok || array.Len == nil {
		return nil, nil, ""
	}
	name := vs.Names[0].Name
	candidate := &skippedVar{name: name, sourceBytes: offset(fset, decl.End()) - offset(fset, decl.Pos())}
	length, ok := arrayLength(array.Len)
	if !ok {
		return nil, candidate, "array length is not a non-negative integer literal"
	}
	elem, ok := array.Elt.(*ast.Ident)
	if !ok {
		return nil, candidate, "element type is not an integer identifier"
	}
	typ, ok := resolveInteger(elem.Name, aliases)
	if !ok {
		return nil, candidate, "element type does not resolve through aliases to a fixed-size integer"
	}
	if length > int(^uint(0)>>1)/typ.width {
		return nil, candidate, "array byte size overflows int"
	}
	size := length * typ.width
	if size < minimumDataBytes {
		return nil, nil, ""
	}
	data := make([]byte, size)
	if len(lit.Elts) > length {
		return nil, candidate, "initializer has more elements than the array length"
	}
	next := 0
	for elementNumber, expr := range lit.Elts {
		if keyed, ok := expr.(*ast.KeyValueExpr); ok {
			indexValue, ok := constants.integer(keyed.Key)
			if !ok {
				return nil, candidate, fmt.Sprintf("element %d has a non-constant index", elementNumber)
			}
			index, ok := constant.Int64Val(indexValue)
			if !ok || index < 0 || index >= int64(length) {
				return nil, candidate, fmt.Sprintf("element %d has an out-of-range index", elementNumber)
			}
			next = int(index)
			expr = keyed.Value
		}
		if next >= length {
			return nil, candidate, "initializer has more elements than the array length"
		}
		value, ok := constants.elementInteger(expr)
		if !ok || !fitsInteger(value, typ) {
			return nil, candidate, fmt.Sprintf("element %d is not a representable constant integer", elementNumber)
		}
		putInteger(data[next*typ.width:], value, typ.width)
		next++
	}
	typeStart := offset(fset, array.Pos())
	typeEnd := offset(fset, array.End())
	docText := ""
	if gen.Doc != nil {
		docText = string(src[offset(fset, gen.Doc.Pos()):offset(fset, gen.Doc.End())]) + "\n"
	} else if vs.Doc != nil {
		docText = string(src[offset(fset, vs.Doc.Pos()):offset(fset, vs.Doc.End())]) + "\n"
	}
	return &movedVar{
		name: name, typeText: string(src[typeStart:typeEnd]), docText: docText, width: typ.width, signed: typ.signed,
		size: size, data: data,
	}, nil, ""
}

func arrayLength(expr ast.Expr) (int, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.ReplaceAll(lit.Value, "_", ""), 0, 64)
	if err != nil || v > uint64(^uint(0)>>1) {
		return 0, false
	}
	return int(v), true
}

func fitsInteger(v constant.Value, typ integerType) bool {
	if i, ok := constant.Int64Val(v); ok {
		if typ.signed {
			if typ.width == 8 {
				return true
			}
			bits := uint(typ.width * 8)
			return i >= -(int64(1)<<(bits-1)) && i <= (int64(1)<<(bits-1))-1
		}
		return i >= 0
	}
	u, ok := constant.Uint64Val(v)
	if !ok || typ.signed {
		return false
	}
	return typ.width == 8 || u < uint64(1)<<(uint(typ.width)*8)
}

func putInteger(dst []byte, v constant.Value, width int) {
	var u uint64
	if n, ok := constant.Uint64Val(v); ok {
		u = n
	} else {
		n, _ := constant.Int64Val(v)
		u = uint64(n)
	}
	switch width {
	case 1:
		dst[0] = byte(u)
	case 2:
		binary.LittleEndian.PutUint16(dst, uint16(u))
	case 4:
		binary.LittleEndian.PutUint32(dst, uint32(u))
	case 8:
		binary.LittleEndian.PutUint64(dst, u)
	}
}

func isImportGuard(decl ast.Decl) bool {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR || len(gen.Specs) == 0 {
		return false
	}
	for _, spec := range gen.Specs {
		for _, name := range spec.(*ast.ValueSpec).Names {
			if name.Name != "_" {
				return false
			}
		}
	}
	return true
}

func offset(fset *token.FileSet, pos token.Pos) int { return fset.PositionFor(pos, false).Offset }

func renderDecl(fset *token.FileSet, decl ast.Decl) ([]byte, error) {
	var out bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
	if err := cfg.Fprint(&out, fset, decl); err != nil {
		return nil, err
	}
	out.WriteString("\n\n")
	return out.Bytes(), nil
}

func renderDecls(fset *token.FileSet, decls []ast.Decl) ([]byte, error) {
	var out bytes.Buffer
	for _, decl := range decls {
		b, err := renderDecl(fset, decl)
		if err != nil {
			return nil, err
		}
		out.Write(b)
	}
	return out.Bytes(), nil
}

func shardDeclarations(decls [][]byte, count int) [][][]byte {
	groups := make([][][]byte, count)
	total := 0
	for _, decl := range decls {
		total += len(decl)
	}
	index, consumed := 0, 0
	for _, decl := range decls {
		for index < count-1 && consumed >= total*(index+1)/count {
			index++
		}
		groups[index] = append(groups[index], decl)
		consumed += len(decl)
	}
	return groups
}

func writePreamble(out *bytes.Buffer, buildLine, packageName string) {
	fmt.Fprintf(out, "%s\n\n// Code generated by splitgo; DO NOT EDIT.\n\npackage %s\n\n", buildLine, packageName)
}

func renderDataFile(buildLine, packageName, base string, vars []movedVar) ([]byte, error) {
	var out bytes.Buffer
	writePreamble(&out, buildLine, packageName)
	hasWide := false
	for _, v := range vars {
		hasWide = hasWide || v.width > 1 || v.signed
	}
	out.WriteString("import (\n\t_ \"embed\"\n")
	if hasWide {
		out.WriteString("\t\"unsafe\"\n")
	}
	out.WriteString(")\n\n")
	fmt.Fprintf(&out, "//go:embed %s_data.bin\nvar _splitgoData []byte\n\n", base)
	for _, v := range vars {
		out.WriteString(v.docText)
		fmt.Fprintf(&out, "var %s %s\n", v.name, v.typeText)
	}
	out.WriteString("\nfunc init() {\n")
	out.WriteString("\t// This generated artifact is little-endian-only, so wider integers are filled by raw byte copy.\n")
	for _, v := range vars {
		if v.width == 1 && !v.signed {
			fmt.Fprintf(&out, "\tcopy(%s[:], _splitgoData[%d:%d])\n", v.name, v.offset, v.offset+v.size)
		} else {
			fmt.Fprintf(&out, "\tcopy(unsafe.Slice((*byte)(unsafe.Pointer(&%s)), unsafe.Sizeof(%s)), _splitgoData[%d:%d])\n", v.name, v.name, v.offset, v.offset+v.size)
		}
	}
	out.WriteString("}\n")
	return format.Source(out.Bytes())
}
