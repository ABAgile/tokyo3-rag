package parser

import (
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/abagile/tokyo3-rag/internal/model"
)

var (
	clojureNsRe      = regexp.MustCompile(`^\s*\(ns\s+([\w.\-/]+)`)
	clojureDefRe     = regexp.MustCompile(`^\s*\(def(?:n|macro|multi|protocol|record|type|-)\s+([\w.\-/?!*<>]+)`)
	clojureRequireRe = regexp.MustCompile(`\[([a-z][\w.\-]+)\s+(?::as\s+\w+)?`)
)

// ParseClojure extracts namespace, function, and protocol nodes from a Clojure
// source file using an s-expression bracket scanner. No CGO required.
func ParseClojure(repoPath string, content []byte) ([]*model.CodeNode, []*model.CodeEdge, error) {
	src := string(content)

	var nodes []*model.CodeNode

	// Detect current namespace from (ns ...) form.
	nsName := "unknown"
	if m := clojureNsRe.FindStringSubmatch(src); m != nil {
		nsName = m[1]
	}

	// Find all top-level forms starting with a recognised def* keyword.
	i := 0
	for i < len(src) {
		// Skip whitespace and comments.
		for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r') {
			i++
		}
		if i >= len(src) {
			break
		}
		// Skip line comments.
		if src[i] == ';' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		// Only interested in top-level list forms.
		if src[i] != '(' {
			// Skip to next newline.
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}

		// Found a top-level form. Find its extent via bracket counting.
		start := i
		startLine := byteOffsetToLine(src, start)
		end := findFormEnd(src, start)
		if end <= start {
			i++
			continue
		}

		formSrc := src[start:end]
		i = end

		// Identify the form type.
		m := clojureDefRe.FindStringSubmatch(formSrc)
		if m == nil {
			continue
		}

		symName := m[1]
		qualified := nsName + "/" + symName
		nodeType := clojureNodeType(formSrc)
		endLine := byteOffsetToLine(src, end-1)

		nodes = append(nodes, &model.CodeNode{
			ID:        uuid.New().String(),
			RepoPath:  repoPath,
			Language:  "clojure",
			NodeType:  nodeType,
			Name:      symName,
			Qualified: qualified,
			Content:   formSrc,
			LineStart: startLine,
			LineEnd:   endLine,
		})
	}

	return nodes, nil, nil
}

// findFormEnd returns the index just past the closing ')' of the form starting at start.
func findFormEnd(src string, start int) int {
	depth := 0
	inStr := false
	escaped := false
	for i := start; i < len(src); i++ {
		c := src[i]
		if escaped {
			escaped = false
			continue
		}
		if inStr {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		case ';':
			// line comment — skip to EOL
			for i < len(src) && src[i] != '\n' {
				i++
			}
		}
	}
	return len(src)
}

func byteOffsetToLine(src string, offset int) int {
	return strings.Count(src[:offset], "\n") + 1
}

func clojureNodeType(formSrc string) string {
	switch {
	case strings.HasPrefix(formSrc, "(defprotocol") || strings.HasPrefix(formSrc, "(definterface"):
		return "interface"
	case strings.HasPrefix(formSrc, "(defrecord") || strings.HasPrefix(formSrc, "(deftype"):
		return "class"
	case strings.HasPrefix(formSrc, "(defn") || strings.HasPrefix(formSrc, "(defmacro") || strings.HasPrefix(formSrc, "(defmulti"):
		return "function"
	case strings.HasPrefix(formSrc, "(ns"):
		return "module"
	default:
		return "function"
	}
}

// suppress unused import warning
var _ = clojureRequireRe
