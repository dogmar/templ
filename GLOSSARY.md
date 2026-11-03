# templ formatting

How templ source whitespace relates to rendered HTML, and what a formatter may change without altering what a template renders.

## Language

**Rendered output**:
The static HTML a template produces, as emitted by the templ generator.
_Avoid_: compiled output, generated HTML (ambiguous with generated Go)

**Render-equivalent**:
Two templ sources are render-equivalent when they produce byte-identical rendered output.
_Avoid_: semantically identical, looks the same

**Inline sibling**:
A node the generator places spaces between: text, a string expression, an inline element, or an `if`/`for`/`switch`.
_Avoid_: inline node, non-block

**Soft boundary**:
A boundary between two inline siblings with whitespace in the source; it renders as a single space however much whitespace (including newlines) it contains.
_Avoid_: space, separator

**Glued boundary**:
A boundary between two inline siblings with no whitespace in the source; it renders as nothing, and must never gain whitespace.
_Avoid_: tight, no-space, hard boundary

**Ignored boundary**:
A boundary whose whitespace never renders, such as next to a block element or just inside an element's tags; it may be freely added to or removed.
_Avoid_: insignificant whitespace

**Fixed boundary**:
A boundary where the kind of whitespace (space versus newline) changes the rendered output, such as text before an inline component call; it must be reproduced exactly.
_Avoid_: special case

**Line-start hazard**:
A word in text that would be parsed as templ syntax if it began a line (e.g. `if `, `for `, `switch `, `@…`, `//`, `/*`), so text must never be broken just before it.
_Avoid_: keyword collision

**Tag break**:
A line break placed inside a tag's delimiters (e.g. `<span⏎>`) so that a glued boundary can be split across lines without gaining whitespace.
_Avoid_: bracket hack, ugly break
