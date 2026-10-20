// Package vdf parses Valve's KeyValues text format, the format Steam writes
// libraryfolders.vdf and appmanifest_*.acf in.
//
// A document is a tree of nodes. A node is either a string value or an
// object holding further nodes:
//
//	"libraryfolders"
//	{
//		"0"
//		{
//			"path"	"/home/ada/.steam/steam"
//			"apps"
//			{
//				"1422450"	"12345678"
//			}
//		}
//	}
package vdf

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// Node is one key of a KeyValues document. A node with Children is an
// object; otherwise Value holds its string.
type Node struct {
	Key      string
	Value    string
	Children []*Node
}

// IsObject reports whether the node holds keys rather than a string value.
func (n *Node) IsObject() bool { return n.Children != nil }

// Get returns the first child named key, or nil when there is none.
func (n *Node) Get(key string) *Node {
	for _, child := range n.Children {
		if child.Key == key {
			return child
		}
	}
	return nil
}

// GetString returns the value of the child named key, or "" when there is
// none. A child that is an object has no value.
func (n *Node) GetString(key string) string {
	if child := n.Get(key); child != nil {
		return child.Value
	}
	return ""
}

// All returns every child named key, in order.
func (n *Node) All(key string) []*Node {
	var found []*Node
	for _, child := range n.Children {
		if child.Key == key {
			found = append(found, child)
		}
	}
	return found
}

// Parse reads a KeyValues document. The root node is unnamed and holds the
// document's top-level keys as children.
func Parse(data []byte) (*Node, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // a UTF-8 BOM
	p := &parser{src: data}
	root := &Node{Children: []*Node{}}
	if _, err := p.parseBody(root); err != nil {
		return nil, err
	}
	return root, nil
}

// ParseFile reads the KeyValues document at path.
func ParseFile(path string) (*Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	root, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return root, nil
}

// parser walks the source, which is small enough to keep in memory whole:
// the largest Steam writes is a few hundred kilobytes.
type parser struct {
	src []byte
	pos int
}

// parseBody reads key/value pairs into parent until the end of the source or
// a closing brace. It reports whether a closing brace ended it.
func (p *parser) parseBody(parent *Node) (bool, error) {
	for {
		key, err := p.token()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		switch key {
		case "}":
			return true, nil
		case "{":
			return false, p.errorf("unexpected %q", key)
		}

		value, err := p.token()
		if err == io.EOF {
			// A key with no value at the end of the file: keep it, empty.
			parent.Children = append(parent.Children, &Node{Key: key})
			return false, nil
		}
		if err != nil {
			return false, err
		}

		switch value {
		case "{":
			node := &Node{Key: key, Children: []*Node{}}
			closed, err := p.parseBody(node)
			if err != nil {
				return false, err
			}
			parent.Children = append(parent.Children, node)
			if !closed {
				return false, nil
			}
		case "}":
			// The key is the last one of the object holding it.
			parent.Children = append(parent.Children, &Node{Key: key})
			return true, nil
		default:
			parent.Children = append(parent.Children, &Node{Key: key, Value: value})
		}
	}
}

// token returns the next token: "{" or "}", or a string, quoted or bare.
func (p *parser) token() (string, error) {
	if err := p.skipSpace(); err != nil {
		return "", err
	}
	switch p.src[p.pos] {
	case '{', '}':
		tok := string(p.src[p.pos])
		p.pos++
		return tok, nil
	case '"':
		return p.quoted()
	default:
		return p.bare(), nil
	}
}

// skipSpace advances past whitespace and // comments.
func (p *parser) skipSpace() error {
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			p.pos++
		case c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '/':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		default:
			return nil
		}
	}
	return io.EOF
}

// quoted reads a double-quoted string, undoing the escapes Steam writes.
func (p *parser) quoted() (string, error) {
	p.pos++ // the opening quote
	var out []byte
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '"' {
			p.pos++
			return string(out), nil
		}
		if c != '\\' {
			out = append(out, c)
			p.pos++
			continue
		}
		p.pos++
		if p.pos >= len(p.src) {
			break
		}
		switch e := p.src[p.pos]; e {
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case 'r':
			out = append(out, '\r')
		case '\\', '"':
			out = append(out, e)
		default:
			out = append(out, '\\', e)
		}
		p.pos++
	}
	return "", p.errorf("unterminated string")
}

// bare reads an unquoted token, as Steam writes numbers.
func (p *parser) bare() string {
	start := p.pos
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; c {
		case ' ', '\t', '\r', '\n', '{', '}', '"':
			return string(p.src[start:p.pos])
		default:
			p.pos++
		}
	}
	return string(p.src[start:p.pos])
}

func (p *parser) errorf(format string, args ...any) error {
	return fmt.Errorf("vdf: offset %d: %s", p.pos, fmt.Sprintf(format, args...))
}
