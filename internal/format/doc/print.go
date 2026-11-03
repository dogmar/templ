package doc

import "unicode/utf8"

type mode int

const (
	modeBreak mode = iota
	modeFlat
)

type cmd struct {
	indent int
	mode   mode
	doc    Doc
}

// Print lays out d within width columns. Indentation is one tab per Indent
// level, counted as TabWidth columns. Lines never have trailing whitespace.
func Print(d Doc, width int) string {
	var out []byte
	pos := 0
	stack := []cmd{{0, modeBreak, d}}

	newline := func(ind int) {
		for len(out) > 0 && (out[len(out)-1] == ' ' || out[len(out)-1] == '\t') {
			out = out[:len(out)-1]
		}
		out = append(out, '\n')
		for range ind {
			out = append(out, '\t')
		}
		pos = ind * TabWidth
	}

	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch d := c.doc.(type) {
		case text:
			out = append(out, d.s...)
			pos += utf8.RuneCountInString(d.s)
		case concat:
			stack = pushReversed(stack, c.indent, c.mode, d.children)
		case indent:
			stack = pushReversed(stack, c.indent+1, c.mode, d.children)
		case line:
			switch {
			case d.kind == lineBlank:
				newline(0)
				newline(c.indent)
			case d.kind == lineHard || c.mode == modeBreak:
				newline(c.indent)
			case d.kind == lineSpace:
				out = append(out, ' ')
				pos++
			}
		case group:
			if c.mode == modeFlat && !d.hard {
				stack = pushReversed(stack, c.indent, modeFlat, d.children)
				break
			}
			m := modeBreak
			if !d.hard && fits(cmd{c.indent, modeFlat, concat{d.children, false}}, stack, width-pos, false) {
				m = modeFlat
			}
			stack = pushReversed(stack, c.indent, m, d.children)
		case fill:
			stack = printFill(stack, c, d, width-pos)
		}
	}
	for len(out) > 0 && (out[len(out)-1] == ' ' || out[len(out)-1] == '\t') {
		out = out[:len(out)-1]
	}
	return string(out)
}

func pushReversed(stack []cmd, ind int, m mode, docs []Doc) []cmd {
	for i := len(docs) - 1; i >= 0; i-- {
		stack = append(stack, cmd{ind, m, docs[i]})
	}
	return stack
}

// printFill pushes the commands for the first content and separator of a
// fill, following Prettier's fill algorithm.
func printFill(stack []cmd, c cmd, f fill, remaining int) []cmd {
	parts := f.parts
	if len(parts) == 0 {
		return stack
	}
	content := parts[0]
	contentFits := fits(cmd{c.indent, modeFlat, content}, nil, remaining, true)
	contentMode := modeBreak
	if contentFits {
		contentMode = modeFlat
	}
	if len(parts) == 1 {
		return append(stack, cmd{c.indent, contentMode, content})
	}
	sep := parts[1]
	if len(parts) == 2 {
		stack = append(stack, cmd{c.indent, contentMode, sep}) // sep mode matches content
		return append(stack, cmd{c.indent, contentMode, content})
	}
	pair := cmd{c.indent, modeFlat, concat{[]Doc{content, sep, parts[2]}, false}}
	sepMode := modeBreak
	if fits(pair, nil, remaining, true) {
		sepMode = modeFlat
	}
	stack = append(stack, cmd{c.indent, c.mode, fill{parts[2:], f.hard}})
	stack = append(stack, cmd{c.indent, sepMode, sep})
	return append(stack, cmd{c.indent, contentMode, content})
}

// fits reports whether next, followed by the remaining stack up to the first
// line break in break mode, fits in the remaining columns.
func fits(next cmd, rest []cmd, remaining int, mustBeFlat bool) bool {
	restIdx := len(rest)
	stack := []cmd{next}
	for remaining >= 0 {
		if len(stack) == 0 {
			if restIdx == 0 {
				return true
			}
			restIdx--
			stack = append(stack, rest[restIdx])
			continue
		}
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch d := c.doc.(type) {
		case text:
			remaining -= utf8.RuneCountInString(d.s)
		case concat:
			stack = pushReversed(stack, c.indent, c.mode, d.children)
		case indent:
			stack = pushReversed(stack, c.indent+1, c.mode, d.children)
		case fill:
			stack = pushReversed(stack, c.indent, c.mode, d.parts)
		case group:
			if mustBeFlat && d.hard {
				return false
			}
			m := c.mode
			if d.hard {
				m = modeBreak
			}
			stack = pushReversed(stack, c.indent, m, d.children)
		case line:
			if c.mode == modeBreak || d.kind == lineHard || d.kind == lineBlank {
				return true
			}
			if d.kind == lineSpace {
				remaining--
			}
		}
	}
	return false
}
