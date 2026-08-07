package gateway_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// dtoSources is where the published wire shapes live. Every `api` package is a DTO package by
// definition; `common` is not one, so the files holding its shared wire types are named instead of
// its whole directory being swept.
var dtoSources = []struct {
	glob string
	// onlyDTOSuffix limits the check to types whose name ends in "DTO", for a file that holds
	// domain types beside its wire ones.
	onlyDTOSuffix bool
}{
	{glob: "../module/*/api/*.go"},
	{glob: "../module/common/optionapi.go"},
	{glob: "../module/common/uploadapi.go"},
	{glob: "../module/common/resource.go", onlyDTOSuffix: true},
}

// TestDTOs_NeverOmitAZeroValue is the rule a client depends on: a zero value is normalised, not
// dropped. An empty list is `[]`, an empty object `{}`, an unset number `0` — never a missing key.
//
// Both halves of the argument are load-bearing. A generated client types a required property as
// non-nullable, so omitting it does not degrade — `Message.refs` carried `omitempty`, went missing
// on the ~all messages that reference nothing, and every chat thread in the app failed to
// deserialise. And where a property is *optional*, "absent" and "empty" become two encodings of
// one fact that every reader has to collapse by hand.
//
// This is checkable only because it is mechanical: the tag is the whole rule. Note that
// `validate:"omitempty,..."` is a different tag and untouched — that one means "skip these rules
// when the field is absent", which is exactly right on a PATCH.
func TestDTOs_NeverOmitAZeroValue(t *testing.T) {
	var checked int
	for _, src := range dtoSources {
		files, err := filepath.Glob(src.glob)
		if err != nil {
			t.Fatalf("glob %s: %v", src.glob, err)
		}
		if len(files) == 0 {
			t.Fatalf("%s matched no files; the DTO check is looking in the wrong place", src.glob)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			checked += checkFileTags(t, file, src.onlyDTOSuffix)
		}
	}
	// A pass that inspected nothing would report success for the wrong reason.
	if checked < 100 {
		t.Fatalf("only %d DTO fields inspected; the walk is not reaching the structs", checked)
	}
}

// checkFileTags reports every json tag in one file that drops a zero value, and answers how many
// tagged fields it looked at.
func checkFileTags(t *testing.T, file string, onlyDTOSuffix bool) int {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	var seen int
	ast.Inspect(parsed, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		structType, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		if onlyDTOSuffix && !strings.HasSuffix(spec.Name.Name, "DTO") {
			return true
		}
		for _, field := range structType.Fields.List {
			if field.Tag == nil {
				continue
			}
			raw, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				continue
			}
			tag := reflect.StructTag(raw).Get("json")
			if tag == "" {
				continue
			}
			// No comma is the common case and still counts as inspected — otherwise a file that
			// already obeys the rule would look unvisited.
			name, opts, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			seen++
			for _, opt := range strings.Split(opts, ",") {
				if opt == "omitempty" || opt == "omitzero" {
					t.Errorf("%s: %s.%s has json:\"%s,%s\" — a DTO must send its zero value, not drop the key",
						fset.Position(field.Tag.Pos()), spec.Name.Name, fieldName(field), name, opt)
				}
			}
		}
		return true
	})
	return seen
}

func fieldName(field *ast.Field) string {
	if len(field.Names) == 0 {
		return "<embedded>"
	}
	return field.Names[0].Name
}
