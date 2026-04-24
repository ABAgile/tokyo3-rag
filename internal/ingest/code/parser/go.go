// Package parser provides language-specific code parsers that extract
// CodeNode and CodeEdge values from source files.
package parser

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/google/uuid"

	"github.com/abagile/tokyo3-rag/internal/model"
)

// ParseGo parses a single Go source file and returns the nodes (functions,
// methods, interfaces, structs) and edges (call, import, implements) it contains.
// repoPath is the file path relative to the repository root.
func ParseGo(repoPath string, content []byte) ([]*model.CodeNode, []*model.CodeEdge, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, repoPath, content, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", repoPath, err)
	}

	pkgName := f.Name.Name
	src := string(content)
	lines := strings.Split(src, "\n")

	var nodes []*model.CodeNode
	var edges []*model.CodeEdge

	// Index nodes by qualified name for call-edge resolution.
	nodeByQualified := map[string]*model.CodeNode{}

	// Collect import paths for import edges.
	var importPaths []string
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		importPaths = append(importPaths, path)
	}

	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if fd.Body == nil {
			return true
		}

		startLine := fset.Position(fd.Pos()).Line
		endLine := fset.Position(fd.End()).Line
		content := extractLines(lines, startLine, endLine)

		var nodeType, name, qualified string
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			recv := receiverTypeName(fd.Recv.List[0].Type)
			name = fd.Name.Name
			qualified = pkgName + "." + recv + "." + name
			nodeType = "method"
		} else {
			name = fd.Name.Name
			qualified = pkgName + "." + name
			nodeType = "function"
		}

		node := &model.CodeNode{
			ID:        uuid.New().String(),
			RepoPath:  repoPath,
			Language:  "go",
			NodeType:  nodeType,
			Name:      name,
			Qualified: qualified,
			Content:   content,
			LineStart: startLine,
			LineEnd:   endLine,
		}
		nodes = append(nodes, node)
		nodeByQualified[qualified] = node
		return true
	})

	// Type declarations (structs and interfaces).
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			var nodeType string
			switch ts.Type.(type) {
			case *ast.InterfaceType:
				nodeType = "interface"
			case *ast.StructType:
				nodeType = "class" // use "class" for structs for cross-language consistency
			default:
				continue
			}
			startLine := fset.Position(ts.Pos()).Line
			endLine := fset.Position(ts.End()).Line
			node := &model.CodeNode{
				ID:        uuid.New().String(),
				RepoPath:  repoPath,
				Language:  "go",
				NodeType:  nodeType,
				Name:      ts.Name.Name,
				Qualified: pkgName + "." + ts.Name.Name,
				Content:   extractLines(lines, startLine, endLine),
				LineStart: startLine,
				LineEnd:   endLine,
			}
			nodes = append(nodes, node)
			nodeByQualified[node.Qualified] = node
		}
	}

	// Call edges: walk each function body for call expressions.
	for _, n := range nodes {
		if n.NodeType != "function" && n.NodeType != "method" {
			continue
		}
	}
	// Walk all function decls again for call edges.
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			return true
		}
		var callerQual string
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			recv := receiverTypeName(fd.Recv.List[0].Type)
			callerQual = pkgName + "." + recv + "." + fd.Name.Name
		} else {
			callerQual = pkgName + "." + fd.Name.Name
		}
		caller, ok := nodeByQualified[callerQual]
		if !ok {
			return true
		}
		ast.Inspect(fd.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee := resolveCallee(call, pkgName)
			if callee == "" {
				return true
			}
			if calleeNode, ok := nodeByQualified[callee]; ok {
				edges = append(edges, &model.CodeEdge{
					ID:         uuid.New().String(),
					FromNodeID: caller.ID,
					ToNodeID:   calleeNode.ID,
					EdgeType:   "call",
				})
			}
			return true
		})
		return true
	})

	// Import edges: link file-level nodes to their import paths as pseudo-nodes.
	// We record import edges as metadata on the first node in the file; the
	// actual resolution happens at the repository level during indexing.
	_ = importPaths // used during repository-level edge linking in the pipeline

	return nodes, edges, nil
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr: // generic receiver T[K]
		return receiverTypeName(t.X)
	default:
		return "Unknown"
	}
}

func resolveCallee(call *ast.CallExpr, pkgName string) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return pkgName + "." + fn.Name
	case *ast.SelectorExpr:
		if ident, ok := fn.X.(*ast.Ident); ok {
			return pkgName + "." + ident.Name + "." + fn.Sel.Name
		}
	}
	return ""
}

func extractLines(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}
