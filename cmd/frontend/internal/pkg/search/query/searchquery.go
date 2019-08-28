// Package query provides facilities for parsing and extracting
// information from search queries.
package query

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/internal/pkg/search/query/syntax"
	"github.com/sourcegraph/sourcegraph/cmd/frontend/internal/pkg/search/query/types"
)

// All field names.
const (
	FieldDefault            = ""
	FieldCase               = "case"
	FieldRepo               = "repo"
	FieldRepoGroup          = "repogroup"
	FieldFile               = "file"
	FieldFork               = "fork"
	FieldArchived           = "archived"
	FieldLang               = "lang"
	FieldType               = "type"
	FieldRepoHasFile        = "repohasfile"
	FieldRepoHasCommitAfter = "repohascommitafter"

	// For diff and commit search only:
	FieldBefore    = "before"
	FieldAfter     = "after"
	FieldAuthor    = "author"
	FieldCommitter = "committer"
	FieldMessage   = "message"

	// Temporary experimental fields:
	FieldIndex   = "index"
	FieldCount   = "count" // Searches that specify `count:` will fetch at least that number of results, or the full result set
	FieldMax     = "max"   // Deprecated alias for count
	FieldTimeout = "timeout"
	FieldReplace = "replace"
)

var (
	regexpNegatableFieldType = types.FieldType{Literal: types.RegexpType, Quoted: types.RegexpType, Negatable: true}
	stringFieldType          = types.FieldType{Literal: types.StringType, Quoted: types.StringType}

	conf = types.Config{
		FieldTypes: map[string]types.FieldType{
			FieldDefault:   {Literal: types.RegexpType, Quoted: types.StringType},
			FieldCase:      {Literal: types.BoolType, Quoted: types.BoolType, Singular: true},
			FieldRepo:      regexpNegatableFieldType,
			FieldRepoGroup: {Literal: types.StringType, Quoted: types.StringType, Singular: true},
			FieldFile:      regexpNegatableFieldType,
			FieldFork:      {Literal: types.StringType, Quoted: types.StringType, Singular: true},
			FieldArchived:  {Literal: types.StringType, Quoted: types.StringType, Singular: true},
			FieldLang:      {Literal: types.StringType, Quoted: types.StringType, Negatable: true},
			FieldType:      stringFieldType,

			FieldRepoHasFile:        regexpNegatableFieldType,
			FieldRepoHasCommitAfter: {Literal: types.StringType, Quoted: types.StringType, Singular: true},

			FieldBefore:    stringFieldType,
			FieldAfter:     stringFieldType,
			FieldAuthor:    regexpNegatableFieldType,
			FieldCommitter: regexpNegatableFieldType,
			FieldMessage:   regexpNegatableFieldType,

			// Experimental fields:
			FieldIndex:   {Literal: types.StringType, Quoted: types.StringType, Singular: true},
			FieldCount:   {Literal: types.StringType, Quoted: types.StringType, Singular: true},
			FieldMax:     {Literal: types.StringType, Quoted: types.StringType, Singular: true},
			FieldTimeout: {Literal: types.StringType, Quoted: types.StringType, Singular: true},
			FieldReplace: {Literal: types.StringType, Quoted: types.StringType, Singular: true},
		},
		FieldAliases: map[string]string{
			"r":        FieldRepo,
			"g":        FieldRepoGroup,
			"f":        FieldFile,
			"l":        FieldLang,
			"language": FieldLang,
			"since":    FieldAfter,
			"until":    FieldBefore,
			"m":        FieldMessage,
			"msg":      FieldMessage,
		},
	}
)

// A Query is the parsed representation of a search query.
type Query struct {
	conf *types.Config // the typechecker config used to produce this query

	*types.Query // the underlying query
}

// ParseAndCheck parses and typechecks a search query using the default
// query type configuration.
func ParseAndCheck(input string) (*Query, error) {
	return parseAndCheck(&conf, input)
}

func parseAndCheck(conf *types.Config, input string) (*Query, error) {
	input = handlePatternType(input)
	syntaxQuery, err := syntax.Parse(input)
	if err != nil {
		return nil, err
	}
	checkedQuery, err := conf.Check(syntaxQuery)
	if err != nil {
		return nil, err
	}
	return &Query{conf: conf, Query: checkedQuery}, nil
}

var fieldRx = regexp.MustCompile(`^-?[a-zA-Z]+:`)

// handlePatternType returns a modified version of the input query where it has
// been either quoted by default, quoted because it has patternType:literal, or
// not quoted because it has patternType:regex.
func handlePatternType(input string) string {
	tokens := tokenize(input)
	isRegex := false
	var tokens2 []string
	for _, t := range tokens {
		switch t {
		case "patternType:regex":
			isRegex = true
		case "patternType:regexp":
			isRegex = true
		case "patternType:literal":
			isRegex = false
		default:
			tokens2 = append(tokens2, t)
		}
	}
	if isRegex {
		// Rebuild the input from the remaining tokens.
		input = strings.TrimSpace(strings.Join(tokens2, ""))
	} else {
		// Sort the tokens into fields and non-fields.
		var fields, nonFields []string
		for _, t := range tokens2 {
			if fieldRx.MatchString(t) {
				fields = append(fields, t)
			} else {
				nonFields = append(nonFields, t)
			}
		}

		// Rebuild the input as fields followed by non-fields quoted together.
		var pieces []string
		if len(fields) > 0 {
			pieces = append(pieces, strings.Join(fields, " "))
		}
		if len(nonFields) > 0 {
			// Count up the number of non-whitespace tokens in the nonFields slice.
			q := strings.Join(nonFields, "")
			q = strings.TrimSpace(q)
			quotedChunk := fmt.Sprintf(`"%s"`, strings.ReplaceAll(q, `"`, `\"`))
			if quotedChunk != `""` {
				pieces = append(pieces, quotedChunk)
			}
		}
		input = strings.Join(pieces, " ")
	}
	return input
}

// tokenize returns a slice of the double-quoted strings, contiguous chunks
// of non-whitespace, and contiguous chunks of whitespace
func tokenize(input string) []string {
	var toks []string
	s := Scanner{r: bufio.NewReader(strings.NewReader(input))}
	for typ, t := s.Scan(); typ != EOF; typ, t = s.Scan() {
		if typ == OTHER || typ == STRING || typ == WS {
			toks = append(toks, t)
		}
	}
	return toks
}

type Token int

const (
	// Special tokens
	EOF Token = iota
	WS

	// Content tokens
	STRING
	OTHER
)

func isWhiteSpace(ch rune) bool {
	return ch == ' ' || ch == '\t' || ch == '\n'
}

var eof = rune(0)

// Scanner is a lexical scanner.
type Scanner struct {
	r *bufio.Reader
}

func (s *Scanner) read() rune {
	ch, _, err := s.r.ReadRune()
	if err != nil {
		return eof
	}
	return ch
}

func (s *Scanner) unread() {
	_ = s.r.UnreadRune()
}

func (s *Scanner) Scan() (tok Token, lit string) {
	// Read the next rune.
	ch := s.read()

	if isWhiteSpace(ch) {
		s.unread()
		return s.scanWhitespace()
	} else if ch == '"' {
		s.unread()
		return s.scanString()
	} else if ch == eof {
		return EOF, ""
	} else {
		s.unread()
		return s.scanOther()
	}
}

func (s *Scanner) scanWhitespace() (Token, string) {
	var buf bytes.Buffer
	_, _ = buf.WriteRune(s.read())
	for {
		if ch := s.read(); ch == eof {
			break
		} else if isWhiteSpace(ch) {
			_, _ = buf.WriteRune(ch)
		} else {
			s.unread()
			break
		}
	}
	return WS, buf.String()
}

func (s *Scanner) scanString() (Token, string) {
	var buf bytes.Buffer
	_, _ = buf.WriteRune(s.read())
	for {
		if ch := s.read(); ch == eof {
			break
		} else if ch == '\\' {
			_, _ = buf.WriteRune(ch)
			if ch2 := s.read(); ch2 == eof {
				break
			} else {
				_, _ = buf.WriteRune(ch2)
			}
		} else if ch == '"' {
			_, _ = buf.WriteRune(ch)
			break
		} else {
			_, _ = buf.WriteRune(ch)
		}
	}
	return STRING, buf.String()
}

func (s *Scanner) scanOther() (Token, string) {
	var buf bytes.Buffer
	_, _ = buf.WriteRune(s.read())
	for {
		if ch := s.read(); ch == eof {
			break
		} else if isWhiteSpace(ch) {
			s.unread()
			break
		} else {
			_, _ = buf.WriteRune(ch)
		}
	}
	return OTHER, buf.String()
}

// BoolValue returns the last boolean value (yes/no) for the field. For example, if the query is
// "foo:yes foo:no foo:yes", then the last boolean value for the "foo" field is true ("yes"). The
// default boolean value is false.
func (q *Query) BoolValue(field string) bool {
	for _, v := range q.Fields[field] {
		if v.Bool != nil {
			return *v.Bool
		}
	}
	return false // default
}

// IsCaseSensitive reports whether the query's expressions are matched
// case sensitively.
func (q *Query) IsCaseSensitive() bool {
	return q.BoolValue(FieldCase)
}

// Values returns the values for the given field.
func (q *Query) Values(field string) []*types.Value {
	if _, ok := q.conf.FieldTypes[field]; !ok {
		panic("no such field: " + field)
	}
	return q.Fields[field]
}

// RegexpPatterns returns the regexp pattern source strings for the given field.
// If the field is not recognized or it is not always regexp-typed, it panics.
func (q *Query) RegexpPatterns(field string) (values, negatedValues []string) {
	fieldType, ok := q.conf.FieldTypes[field]
	if !ok {
		panic("no such field: " + field)
	}
	if fieldType.Literal != types.RegexpType || fieldType.Quoted != types.RegexpType {
		panic("field is not always regexp-typed: " + field)
	}

	for _, v := range q.Fields[field] {
		s := v.Regexp.String()
		if v.Not() {
			negatedValues = append(negatedValues, s)
		} else {
			values = append(values, s)
		}
	}
	return
}

// StringValues returns the string values for the given field. If the field is
// not recognized or it is not always string-typed, it panics.
func (q *Query) StringValues(field string) (values, negatedValues []string) {
	fieldType, ok := q.conf.FieldTypes[field]
	if !ok {
		panic("no such field: " + field)
	}
	if fieldType.Literal != types.StringType || fieldType.Quoted != types.StringType {
		panic("field is not always string-typed: " + field)
	}

	for _, v := range q.Fields[field] {
		if v.Not() {
			negatedValues = append(negatedValues, *v.String)
		} else {
			values = append(values, *v.String)
		}
	}
	return
}

// StringValue returns the string value for the given field.
// It panics if the field is not recognized, it is not always string-typed, or it is not singular.
func (q *Query) StringValue(field string) (value, negatedValue string) {
	fieldType, ok := q.conf.FieldTypes[field]
	if !ok {
		panic("no such field: " + field)
	}
	if fieldType.Literal != types.StringType || fieldType.Quoted != types.StringType {
		panic("field is not always string-typed: " + field)
	}
	if !fieldType.Singular {
		panic("field is not singular: " + field)
	}
	if len(q.Fields[field]) == 0 {
		return "", ""
	}
	v := q.Fields[field][0]
	if v.Not() {
		return "", *v.String
	}
	return *v.String, ""
}
