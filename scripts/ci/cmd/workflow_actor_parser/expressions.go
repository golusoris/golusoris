// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"strings"

	"github.com/rhysd/actionlint"
)

func reference(node actionlint.ExprNode) string {
	parts := make([]string, 0, 8)
	for depth := 0; node != nil && depth < 128; depth++ {
		switch n := node.(type) {
		case *actionlint.VariableNode:
			parts = append(parts, n.Name)
			reversed := make([]string, len(parts))
			for i := 0; i < len(parts) && i < 128; i++ {
				reversed[i] = parts[len(parts)-1-i]
			}
			return strings.ToLower(strings.Join(reversed, "."))
		case *actionlint.ObjectDerefNode:
			parts, node = append(parts, n.Property), n.Receiver
		case *actionlint.ArrayDerefNode:
			parts, node = append(parts, "*"), n.Receiver
		case *actionlint.IndexAccessNode:
			key := "[computed]"
			if literal, ok := n.Index.(*actionlint.StringNode); ok {
				key = literal.Value
			}
			parts, node = append(parts, key), n.Operand
		default:
			return ""
		}
	}
	return "[depth-bound]"
}

type expressionItem struct {
	node     actionlint.ExprNode
	receiver bool
}

func expressionChildren(node actionlint.ExprNode) []expressionItem {
	switch n := node.(type) {
	case *actionlint.ObjectDerefNode:
		return []expressionItem{{n.Receiver, true}}
	case *actionlint.ArrayDerefNode:
		return []expressionItem{{n.Receiver, true}}
	case *actionlint.IndexAccessNode:
		return []expressionItem{{n.Operand, true}, {n.Index, false}}
	case *actionlint.NotOpNode:
		return []expressionItem{{n.Operand, false}}
	case *actionlint.CompareOpNode:
		return []expressionItem{{n.Left, false}, {n.Right, false}}
	case *actionlint.LogicalOpNode:
		return []expressionItem{{n.Left, false}, {n.Right, false}}
	case *actionlint.FuncCallNode:
		items := make([]expressionItem, 0, len(n.Args))
		for _, arg := range n.Args[:min(len(n.Args), maxNodes)] {
			items = append(items, expressionItem{arg, false})
		}
		return items
	default:
		return nil
	}
}

func expressionFacts(root actionlint.ExprNode) ([]string, []string, string) {
	stack := []expressionItem{{root, false}}
	refs, calls := []string{}, []string{}
	for count := 0; len(stack) > 0 && count < maxNodes; count++ {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !item.receiver {
			if path := reference(item.node); path != "" {
				refs = append(refs, path)
			}
		}
		if call, ok := item.node.(*actionlint.FuncCallNode); ok {
			calls = append(calls, strings.ToLower(call.Callee))
		}
		stack = append(stack, expressionChildren(item.node)...)
	}
	if len(stack) > 0 {
		return nil, nil, "workflow parser: expression node bound exceeded"
	}
	return refs, calls, ""
}

func parseExpression(text string) ([]string, []string, int, string) {
	tokens, _, lexErr := actionlint.LexExpression(text)
	if lexErr != nil {
		return nil, nil, 0, fmt.Sprintf("workflow parser: expression lexer: %v", lexErr)
	}
	if len(tokens) > 4096 {
		return nil, nil, 0, "workflow parser: expression token bound exceeded"
	}
	depth := 0
	for _, token := range tokens[:min(len(tokens), 4096)] {
		if token.Kind == actionlint.TokenKindLeftParen || token.Kind == actionlint.TokenKindLeftBracket || token.Kind == actionlint.TokenKindNot {
			depth++
		}
	}
	if depth > 128 {
		return nil, nil, 0, "workflow parser: expression depth bound exceeded"
	}
	lexer := actionlint.NewExprLexer(text)
	root, err := actionlint.NewExprParser().Parse(lexer)
	if err != nil {
		return nil, nil, 0, fmt.Sprintf("workflow parser: expression AST: %v", err)
	}
	refs, calls, issue := expressionFacts(root)
	return refs, calls, lexer.Offset(), issue
}

func scalarExpressions(s scalar) ([]string, []string, string) {
	text := s.Text
	implicit := len(s.Path) > 0 && s.Path[len(s.Path)-1] == "if" && !strings.Contains(text, "${{")
	if implicit {
		refs, calls, _, issue := parseExpression(text + " }}")
		return refs, calls, issue
	}
	refs, calls := []string{}, []string{}
	for range maxBytes {
		index := strings.Index(text, "${{")
		if index < 0 {
			return refs, calls, ""
		}
		text = text[index+3:]
		found, functions, consumed, issue := parseExpression(text)
		if issue != "" || consumed <= 0 || consumed > len(text) {
			return refs, calls, "workflow parser: invalid expression: " + issue
		}
		refs, calls = append(refs, found...), append(calls, functions...)
		text = text[consumed:]
	}
	return nil, nil, "workflow parser: expression span bound exceeded"
}
