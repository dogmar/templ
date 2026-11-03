# Render-equivalent `templ fmt` and a dprint plugin: design

Decided in a grilling session on 2026-11-02. Terms are defined in `GLOSSARY.md`, and the rationale is in `docs/adr/0001-render-equivalent-formatting.md`.

## Problem

`templ fmt` adds newlines at glued boundaries, and the generator renders those newlines as a space. Two examples:

- `<span>a</span><span>⏎b⏎</span>` renders as `a b` after formatting instead of `ab`.
- `{ "a" }<a>⏎link⏎</a>` gains a leading space.

The cause is that the formatter's `isBlockNode` (`parser/v2/types.go`) disagrees with the generator's `isTrailingSpaceNeeded` (`generator/generator.go`).

## Contract

- The layout step must be **render-equivalent**: the generator must produce the same rendered output for the tree before layout and for the re-parsed output after it. The generator and the parser are not changed.
- Embedded languages are formatted by their own formatters, which run before layout and are trusted:
  - Go: gofmt and goimports
  - `<script>`/`<style>` and attribute values: Prettier on the CLI and LSP, dprint host formatting in the plugin

## Formatter (Go fork)

1. **Shared boundary classifier** in `parser/v2`, used by both the generator and the formatter. It classifies each boundary between siblings as one of four kinds:
   - **Glued:** no whitespace in the source; renders as nothing.
   - **Soft:** any whitespace; renders as one space. It may become a space or a newline.
   - **Ignored:** never renders. Whitespace may be added or removed freely.
   - **Fixed:** text before an inline `@component`. A space and a newline render differently here, so the whitespace is kept exactly.
2. **Document IR** (Prettier/Wadler style), with layout like oxfmt's JSX:
   - **Width:** default 100, set with `templ fmt -width N` and the LSP's `-width N`. Indentation is tabs.
   - **Groups:** a group fits on one line or breaks.
   - **Opening tags:** when an opening tag breaks, each attribute goes on its own line and `>` goes on its own line.
   - **Children:** in a broken group, each child goes on its own line.
   - **Text:** refilled word by word.
3. **Glued boundaries** break only with a **tag break**: a newline inside the next opening tag, before its first attribute or before `>`. If there's no following opening tag (e.g. `</a>.`), the boundary can't break and the line may overflow. The templ parser is not changed.
4. **Text refilling** breaks only at single spaces between words. Two rules restrict this:
   - Runs of two or more spaces, or tabs, inside text are kept exactly and can't be broken.
   - Text is never broken just before a **line-start hazard**: `if `, `for `, `switch `, a word starting with `@`, `//`, or `/*`.
5. **Other layout rules:**
   - `if`/`for`/`switch` start on their own line unless the boundary before them is glued.
   - A single blank line between siblings is kept; runs of blank lines collapse to one.
   - `<pre>` and `<textarea>` are formatted like any other element, because the generator already collapses their whitespace. Literal whitespace has to be written as `{ "…" }`.
   - Go code is formatted by gofmt only and is never broken to fit the width.
   - goimports and Prettier (CLI and LSP) work as they do today.
6. **Self-check** runs before anything is written. If it fails:
   - The CLI leaves the file untouched, prints the file and the first rendered difference, carries on with the other files, and exits non-zero.
   - The LSP returns no edits and logs the difference.
7. **Replaces the old layout** in both `templ fmt` and LSP formatting.
8. **Tests:**
   - Every format fixture is checked for render-equivalence and idempotence.
   - A fuzz test.
   - Every `generator/test-*/template.templ` must format render-equivalently.
9. **Docs:** update the formatting docs with what a tag break (`><`) means, the self-check, and literal whitespace in `<pre>`/`<textarea>`.

## dprint plugin (Go fork)

- `cmd/dprint-plugin-templ`: a Go `wasip1` reactor (`-buildmode=c-shared`, `//go:wasmexport`) implementing dprint's Wasm plugin schema v4. It needs dprint ≥ 0.61 for WASI.
- It shares the formatter package with `templ fmt`.
- **`lineWidth`:** the plugin's own setting, then dprint's global `lineWidth` if one is set, otherwise 100 (oxfmt's default).
- **Embedded content:** `<script>`/`<style>` go through dprint host formatting, or stay verbatim if none is configured. goimports is off.
- **Self-check failure:** returned as a format error.
- **Spike first:** prove it builds, loads and formats at acceptable size and speed. If it doesn't, the fallback is `dprint-plugin-exec` running `templ fmt -stdin`.

## Out of scope

- Any change to the generator or the parser.
- A Rust port. If it happens later, it goes in a separate repo, uses the Go formatter as its oracle, and decides parity and goimports at that point.
