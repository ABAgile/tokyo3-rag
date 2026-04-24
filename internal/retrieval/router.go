package retrieval

import (
	"strings"
	"unicode"
)

// QueryType classifies what kind of knowledge source to search.
type QueryType int

const (
	QueryTypeBoth QueryType = iota
	QueryTypeDoc
	QueryTypeCode
)

// ClassifyQuery uses heuristics to decide whether a query is primarily about
// code, documents, or both. This runs in the hot path so it must be cheap.
func ClassifyQuery(query string) QueryType {
	q := strings.ToLower(query)

	codeSignals := 0
	docSignals := 0

	// Code signals: file extensions, programming keywords, symbol syntax.
	codePatterns := []string{
		".go", ".rb", ".clj", ".cljs", ".rake",
		"func ", "def ", "defn ", "class ", "module ",
		"interface ", "struct ", "method ", "function ",
		"package ", "namespace ", "require ", "import ",
		"variable ", "parameter ", "argument ", "return type",
		"calls ", "called by", "implements ", "inherits",
	}
	for _, p := range codePatterns {
		if strings.Contains(q, p) {
			codeSignals++
		}
	}

	// Detect camelCase or snake_case symbols.
	if hasCamelCase(query) || hasSnakeCase(query) {
		codeSignals++
	}

	// Doc signals: document-oriented vocabulary.
	docPatterns := []string{
		"document", "wiki", "guide", "how-to", "tutorial",
		"policy", "procedure", "report", "meeting", "spec",
		"design doc", "runbook", "readme", "changelog", "release notes",
		"according to", "the document", "the report",
	}
	for _, p := range docPatterns {
		if strings.Contains(q, p) {
			docSignals++
		}
	}

	switch {
	case codeSignals > 0 && docSignals == 0:
		return QueryTypeCode
	case docSignals > 0 && codeSignals == 0:
		return QueryTypeDoc
	default:
		return QueryTypeBoth
	}
}

func hasCamelCase(s string) bool {
	for i := 1; i < len(s)-1; i++ {
		if unicode.IsLower(rune(s[i-1])) && unicode.IsUpper(rune(s[i])) {
			return true
		}
	}
	return false
}

func hasSnakeCase(s string) bool {
	for w := range strings.FieldsSeq(s) {
		if strings.Count(w, "_") >= 2 && len(w) > 4 {
			return true
		}
	}
	return false
}
