// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jcs

// cursor tracks the next child of a container being written.
type cursor struct {
	node int
	next int
}

// emit writes the tree rooted at root depth-first with an explicit stack
// (HISS-01); each node is pushed once, so 2*len(nodes) steps bound the loop.
func (p *parser) emit(root, sizeHint int) []byte {
	out := make([]byte, 0, sizeHint)
	stack := make([]cursor, 0, 16)
	stack = append(stack, cursor{node: root, next: -1})
	for steps := 0; steps < 2*len(p.nodes)+1 && len(stack) > 0; steps++ {
		top := &stack[len(stack)-1]
		n := &p.nodes[top.node]
		if n.kind == scalar {
			out = append(out, n.raw...)
			stack = stack[:len(stack)-1]
			continue
		}
		if top.next < 0 {
			out = append(out, opener(n.kind))
			top.next = 0
		}
		child, ok := p.nextChild(n, top, &out)
		if !ok {
			out = append(out, closer(n.kind))
			stack = stack[:len(stack)-1]
			continue
		}
		stack = append(stack, cursor{node: child, next: -1})
	}
	return out
}

// nextChild writes the separator and member name before the next child and
// returns its index, or false when the container is exhausted.
func (p *parser) nextChild(n *node, c *cursor, out *[]byte) (int, bool) {
	count := len(n.elems)
	if n.kind == object {
		count = len(n.members)
	}
	if c.next >= count {
		return 0, false
	}
	if c.next > 0 {
		*out = append(*out, ',')
	}
	i := c.next
	c.next++
	if n.kind == array {
		return n.elems[i], true
	}
	*out = append(*out, n.members[i].enc...)
	*out = append(*out, ':')
	return n.members[i].child, true
}

func opener(k kind) byte {
	if k == object {
		return '{'
	}
	return '['
}

func closer(k kind) byte {
	if k == object {
		return '}'
	}
	return ']'
}
