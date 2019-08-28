package query

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

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
// of non-whitespace, and contiguous chunks of whitespace in the input.
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
