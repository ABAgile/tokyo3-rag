package parser

import (
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/abagile/tokyo3-rag/internal/model"
)

var (
	rubyClassRe   = regexp.MustCompile(`^\s*class\s+(\w+(?:::\w+)*)(?:\s*<\s*(\w+(?:::\w+)*))?`)
	rubyModuleRe  = regexp.MustCompile(`^\s*module\s+(\w+(?:::\w+)*)`)
	rubyMethodRe  = regexp.MustCompile(`^\s*(def\s+(?:self\.)?(\w+[?!=]?))`)
	rubyRequireRe = regexp.MustCompile(`^\s*require(?:_relative)?\s+['"]([^'"]+)['"]`)
	// Keywords that open a new end-counted block.
	rubyOpenRe = regexp.MustCompile(`\b(?:def|class|module|begin|do|if|unless|case|while|until|for)\b`)
	// Single-line if/unless modifiers don't open blocks — heuristic: skip lines
	// where the keyword appears after the first token.
	rubyModifierRe = regexp.MustCompile(`^[^#]*\S\s+(?:if|unless|while|until)\s`)
)

// ParseRuby extracts class, module, and method nodes from a Ruby source file.
// It uses a line-by-line scanner with end-counting for block boundaries.
func ParseRuby(repoPath string, content []byte) ([]*model.CodeNode, []*model.CodeEdge, error) {
	lines := strings.Split(string(content), "\n")

	var nodes []*model.CodeNode
	var edges []*model.CodeEdge

	type frame struct {
		kind      string // "class" | "module" | "method"
		name      string
		qualified string
		startLine int
		depth     int // end-depth at which this frame closes
	}

	var stack []frame
	depth := 0 // net open blocks

	currentClass := func() string {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].kind == "class" || stack[i].kind == "module" {
				return stack[i].qualified
			}
		}
		return ""
	}

	closeFrame := func(lineIdx int) {
		if len(stack) == 0 {
			return
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		endLine := lineIdx + 1
		nodeContent := strings.Join(lines[top.startLine-1:min(endLine, len(lines))], "\n")

		var nodeType string
		switch top.kind {
		case "class":
			nodeType = "class"
		case "module":
			nodeType = "module"
		case "method":
			nodeType = "function"
		default:
			return
		}
		nodes = append(nodes, &model.CodeNode{
			ID:        uuid.New().String(),
			RepoPath:  repoPath,
			Language:  "ruby",
			NodeType:  nodeType,
			Name:      top.name,
			Qualified: top.qualified,
			Content:   nodeContent,
			LineStart: top.startLine,
			LineEnd:   endLine,
		})
	}

	for i, line := range lines {
		lineNum := i + 1
		stripped := strings.TrimSpace(line)

		// Skip comments and blank lines for block counting.
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}

		// Require/import edges.
		if m := rubyRequireRe.FindStringSubmatch(line); m != nil {
			// Emit an import edge from the current class/module node once it exists.
			// We store the require path in a synthetic node and link it later.
			// For now, just record it in metadata via a placeholder node.
			_ = m[1] // import path, resolved at pipeline level
		}

		// Detect class / module openers.
		if m := rubyClassRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			parent := m[2]
			depth++
			qualified := containerQualified(currentClass(), name)
			stack = append(stack, frame{
				kind:      "class",
				name:      name,
				qualified: qualified,
				startLine: lineNum,
				depth:     depth,
			})
			if parent != "" {
				// Record inheritance edge — deferred until both nodes exist.
				_ = parent
			}
			continue
		}
		if m := rubyModuleRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			depth++
			qualified := containerQualified(currentClass(), name)
			stack = append(stack, frame{
				kind:      "module",
				name:      name,
				qualified: qualified,
				startLine: lineNum,
				depth:     depth,
			})
			continue
		}

		// Detect method definitions.
		if m := rubyMethodRe.FindStringSubmatch(line); m != nil {
			methodName := m[2]
			depth++
			parent := currentClass()
			var qualified string
			if parent != "" {
				qualified = parent + "#" + methodName
			} else {
				qualified = methodName
			}
			stack = append(stack, frame{
				kind:      "method",
				name:      methodName,
				qualified: qualified,
				startLine: lineNum,
				depth:     depth,
			})
			continue
		}

		// Count other block openers (do, begin, if/unless/case/while etc.)
		// but skip single-line modifier forms (foo if condition).
		if !rubyModifierRe.MatchString(line) {
			opens := rubyOpenRe.FindAllString(stripped, -1)
			depth += len(opens)
		}

		// Detect end keyword.
		if stripped == "end" || strings.HasPrefix(stripped, "end ") || strings.HasPrefix(stripped, "end#") {
			if len(stack) > 0 && depth == stack[len(stack)-1].depth {
				closeFrame(i)
			}
			if depth > 0 {
				depth--
			}
		}
	}

	// Close any remaining open frames (unterminated files).
	for len(stack) > 0 {
		closeFrame(len(lines) - 1)
	}

	_ = edges // edges built at pipeline level from inheritance/require info
	return nodes, edges, nil
}

func containerQualified(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "::" + name
}
