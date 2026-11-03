# templ fmt must be render-equivalent, and reflows using tag breaks

`templ fmt` was unsafe: it added line breaks at glued boundaries (e.g. before a multi-line inline element or an `if`), which the generator renders as a space. We decided that the formatter's contract is render-equivalence: the formatted file must produce byte-identical rendered output under the current generator. The generator's whitespace rules are not changed. The formatter and the generator share one boundary classifier (glued, soft, ignored, fixed) so they can't drift apart again, and `templ fmt` checks every file before writing it by generating it before and after layout and comparing the output. If they differ, the file is left untouched and the run fails. Embedded languages (Go via gofmt and goimports, `<script>`/`<style>` and attributes via Prettier or dprint host formatting) are left to their own formatters, which we trust to preserve meaning. The check covers templ's layout step, which runs after them.

Within that contract, layout is width-driven in the style of oxfmt/Prettier JSX: groups break, attributes go one per line, and text is refilled. A glued boundary is split only with a tag break (`<span⏎>`), and the `><` doubles as a visible "no space here" marker. We do not change the templ parser, so a glued boundary that has no following opening tag (e.g. `</a>.`) stays on one line even when that overflows the width.

## Considered Options

- **Glue only** (never break a glued boundary): safe, but it can't produce oxfmt-like layout.
- **Allow `</tag⏎>` in the parser**: would let every glued boundary break, but it's a language change, rejected to keep the work formatter-only.
- **JSX-style semantics** (newlines insignificant, explicit `{" "}` for a space): would change how existing templates render.
- **Collapse runs of spaces in text**: browsers usually collapse them, but `white-space: pre*`, `textContent` and form fields can show the difference and the formatter can't see them, so runs of spaces inside text are kept exactly and can't be broken.

## Consequences

- The generator's quirks become formatting constraints. Text before an inline `@component` is a fixed boundary, and `if `/`for `/`switch `/`@`/`//`/`/*` are line-start hazards that text must not be broken before.
- `<pre>` and `<textarea>` are formatted like any other element, because the generator already collapses their whitespace. Literal whitespace has to be written as a string expression, e.g. `{ "  a\n  b" }`.
- The first run reformats most existing files.
