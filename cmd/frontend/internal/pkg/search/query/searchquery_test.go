package query

import (
	"reflect"
	"testing"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/internal/pkg/search/query/types"
)

func TestQuery_IsCaseSensitive(t *testing.T) {
	conf := types.Config{
		FieldTypes: map[string]types.FieldType{
			FieldCase: {Literal: types.BoolType, Quoted: types.BoolType, Singular: true},
		},
	}

	t.Run("yes", func(t *testing.T) {
		query, err := parseAndCheck(&conf, "case:yes")
		if err != nil {
			t.Fatal(err)
		}
		if !query.IsCaseSensitive() {
			t.Error("IsCaseSensitive() == false, want true")
		}
	})

	t.Run("no (explicit)", func(t *testing.T) {
		query, err := parseAndCheck(&conf, "case:no")
		if err != nil {
			t.Fatal(err)
		}
		if query.IsCaseSensitive() {
			t.Error("IsCaseSensitive() == true, want false")
		}
	})

	t.Run("no (default)", func(t *testing.T) {
		query, err := parseAndCheck(&conf, "")
		if err != nil {
			t.Fatal(err)
		}
		if query.IsCaseSensitive() {
			t.Error("IsCaseSensitive() == true, want false")
		}
	})
}

func TestQuery_RegexpPatterns(t *testing.T) {
	conf := types.Config{
		FieldTypes: map[string]types.FieldType{
			"r": regexpNegatableFieldType,
			"s": {Literal: types.RegexpType, Quoted: types.StringType},
		},
	}

	t.Run("for regexp field", func(t *testing.T) {
		query, err := parseAndCheck(&conf, "r:a r:b -r:c")
		if err != nil {
			t.Fatal(err)
		}
		v, nv := query.RegexpPatterns("r")
		if want := []string{"a", "b"}; !reflect.DeepEqual(v, want) {
			t.Errorf("got values %q, want %q", v, want)
		}
		if want := []string{"c"}; !reflect.DeepEqual(nv, want) {
			t.Errorf("got negated values %q, want %q", nv, want)
		}
	})

	t.Run("for unrecognized field", func(t *testing.T) {
		query, err := parseAndCheck(&conf, "")
		if err != nil {
			t.Fatal(err)
		}
		checkPanic(t, "no such field: z", func() {
			query.RegexpPatterns("z")
		})
	})

	t.Run("for non-regexp field", func(t *testing.T) {
		query, err := parseAndCheck(&conf, "s:a")
		if err != nil {
			t.Fatal(err)
		}
		checkPanic(t, "field is not always regexp-typed: s", func() {
			query.RegexpPatterns("s")
		})
	})
}

func TestQuery_StringValues(t *testing.T) {
	conf := types.Config{
		FieldTypes: map[string]types.FieldType{
			"s": {Literal: types.StringType, Quoted: types.StringType, Negatable: true},
			"r": {Literal: types.RegexpType, Quoted: types.StringType},
		},
	}

	t.Run("for string field", func(t *testing.T) {
		query, err := parseAndCheck(&conf, "s:a s:b -s:c")
		if err != nil {
			t.Fatal(err)
		}
		v, nv := query.StringValues("s")
		if want := []string{"a", "b"}; !reflect.DeepEqual(v, want) {
			t.Errorf("got values %q, want %q", v, want)
		}
		if want := []string{"c"}; !reflect.DeepEqual(nv, want) {
			t.Errorf("got negated values %q, want %q", nv, want)
		}
	})

	t.Run("for unrecognized field", func(t *testing.T) {
		query, err := parseAndCheck(&conf, "")
		if err != nil {
			t.Fatal(err)
		}
		checkPanic(t, "no such field: z", func() {
			query.StringValues("z")
		})
	})

	t.Run("for non-string field", func(t *testing.T) {
		query, err := parseAndCheck(&conf, "r:a")
		if err != nil {
			t.Fatal(err)
		}
		checkPanic(t, "field is not always string-typed: r", func() {
			query.StringValues("r")
		})
	})
}

func checkPanic(t *testing.T, msg string, f func()) {
	t.Helper()
	defer func() {
		if e := recover(); e == nil {
			t.Error("no panic")
		} else if e.(string) != msg {
			t.Errorf("got panic %q, want %q", e, msg)
		}
	}()
	f()
}

func TestHandlePatternType(t *testing.T) {
	tcs := []struct {
		input string
		want  string
	}{
		{"", ""},
		{" ", ""},
		{"  ", ""},
		{`a`, `"a"`},
		{` a`, `"a"`},
		{`a `, `"a"`},
		{` a `, `"a"`},
		{`a b`, `"a b"`},
		{`a  b`, `"a  b"`},
		{"a\tb", "\"a\tb\""},
		{`:`, `":"`},
		{`:=`, `":="`},
		{`:= range`, `":= range"`},
		{"`", "\"`\""},
		{`'`, `"'"`},
		{`:`, `":"`},
		{"f:a", "f:a"},
		{`"f:a"`, `"\"f:a\""`},
		{"r:b r:c", "r:b r:c"},
		{"r:b -r:c", "r:b -r:c"},
		{"patternType:regex", ""},
		{"patternType:regexp", ""},
		{"patternType:literal", ""},
		{"patternType:regexp patternType:literal .*", `".*"`},
		{"patternType:regexp patternType:literal .*", `".*"`},
		{`patternType:regexp "patternType:literal"`, `"patternType:literal"`},
		{`patternType:regexp "patternType:regexp"`, `"patternType:regexp"`},
		{`patternType:literal "patternType:regexp"`, `"\"patternType:regexp\""`},
		{"patternType:regexp .*", ".*"},
		{"patternType:regexp .* ", ".*"},
		{"patternType:regexp .* .*", ".* .*"},
		{"patternType:regexp .*  .*", ".*  .*"},
		{"patternType:regexp .*\t.*", ".*\t.*"},
		{".* patternType:regexp .*", ".*  .*"},
		{".* patternType:regexp", ".*"},
		{"patternType:literal .*", `".*"`},
		{`lang:go func main`, `lang:go "func main"`},
		{`lang:go func  main`, `lang:go "func  main"`},
		{`func main lang:go`, `lang:go "func main"`},
		{`func  main lang:go`, `lang:go "func  main"`},
		{`func lang:go main`, `lang:go "func  main"`},
	}
	for _, tc := range tcs {
		t.Run(tc.input, func(t *testing.T) {
			out := handlePatternType(tc.input)
			if out != tc.want {
				t.Errorf("handlePatternType(%q) = %q, want %q", tc.input, out, tc.want)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	tcs := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{" ", []string{" "}},
		{"a", []string{"a"}},
		{" a", []string{" ", "a"}},
		{"a ", []string{"a", " "}},
		{"a b", []string{"a", " ", "b"}},
		{`"`, []string{`"`}},
		{`""`, []string{`""`}},
		{`" " "`, []string{`" "`, " ", `"`}},
		{`" " " "`, []string{`" "`, " ", `" "`}},
		{`"\""`, []string{`"\""`}},
		{`"\""`, []string{`"\""`}},
		{`"\"" "\""`, []string{`"\""`, " ", `"\""`}},
		{`f:a "r:b"`, []string{`f:a`, " ", `"r:b"`}},
		{"//", []string{"//"}},
		{"/**/", []string{"/**/"}},
	}

	for _, tc := range tcs {
		t.Run(tc.input, func(t *testing.T) {
			toks := tokenize(tc.input)
			if !reflect.DeepEqual(toks, tc.want) {
				t.Errorf("tokenize(%q) = %q, want %q", tc.input, toks, tc.want)
			}
		})
	}
}
