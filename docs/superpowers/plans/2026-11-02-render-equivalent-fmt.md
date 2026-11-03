# Render-equivalent `templ fmt` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `templ fmt`'s layout with a width-driven, oxfmt-style printer that can never change rendered output, and that checks this itself before writing any file.

**Architecture:**
- A characterization census pins how the current generator renders whitespace at every kind of boundary.
- A shared `parser.ClassifyBoundary` encodes the census; the generator and the new printer both call it.
- The new printer builds a Prettier/Wadler document IR (`internal/format/doc`) from each `HTMLTemplate` body. Everything outside template bodies keeps today's `Write` methods.
- `internal/rendercheck` compares generator output before and after layout. `format.Templ` refuses to return output that fails the comparison.

**Tech Stack:** Go 1.26 (`go.mod`), `github.com/a-h/parse`, `go/format`, txtar fixtures, `go test -fuzz`.

**Spec:** `docs/superpowers/specs/2026-11-02-render-equivalent-fmt-design.md`. Also read `GLOSSARY.md` and `docs/adr/0001-render-equivalent-formatting.md`.

## Global Constraints

- No changes to rendered output. Generator behaviour must stay byte-for-byte the same: all `generator/test-*` render tests pass unchanged.
- No changes to the templ parser grammar (`parser/v2/*parser.go`).
- Default width: `100`. Flag name: `-width` on both `templ fmt` and `templ lsp`.
- Indentation is one tab per level. For width measurement a tab counts as `4` columns (`doc.TabWidth = 4`).
- Line-start hazards, verbatim: a word equal to `if`, `for`, or `switch` followed by a space or end of text; any word starting with `@`; any word starting with `//` or `/*`.
- The self-check compares the tree after goimports and Prettier with the re-parsed printed output. Embedded languages are not compared byte-for-byte.
- Terms in identifiers and messages follow `GLOSSARY.md`: glued, soft, ignored, fixed, tag break, render-equivalent.

## Review Focus

- **Text before an HTML comment.** `text <!-- c -->` keeps its space inside the text value. Breaking there would turn `text <!--` into `text<!--`. Pinned in Task 2's census and Task 5's fixture `text_before_comment_keeps_space.txt`.
- **The last child inside an `if`/`for`/`switch` body.** Its trailing whitespace renders based on the node *after* the statement (`generator.go` passes `nextNode` into bodies). Pinned in Task 2's census, context `statementBodyEnd`.
- **Self-closing `@component` after text, written on a new line.** It renders glued, so it must stay on its own line, and text written as `text @c()` must stay on one line. Pinned in Task 2's census and Task 5's fixture `component_after_text_fixed_boundary.txt`.
- **A long glued run that can't break** (`</a>.`, or `{ x }` after a closing tag). Output overflows the width, but the formatter must neither error nor oscillate between runs. Pinned in Task 4's fixture `glued_close_tag_text_overflows.txt`, checked for idempotence.
- **Files the self-check rejects in the CLI.** The file must stay byte-identical on disk, other files must still be formatted, and the exit code must be non-zero. Pinned in Task 6's `TestRunLeavesFileUntouchedOnRenderMismatch`.

---

### Task 1: Render-equivalence oracle

**Files:**
- Create: `internal/rendercheck/rendercheck.go`
- Test: `internal/rendercheck/rendercheck_test.go`

**Interfaces:**
- Produces:
  - `func Render(tf *parser.TemplateFile) (string, error)`: the normalized generator output. It runs `generator.Generate(tf, w, generator.WithSkipCodeGeneratedComment(), generator.WithFileName("x.templ"))`, deletes every `Line: \d+, Col: \d+` match, then runs `go/format.Source` on the result.
  - `func Equivalent(before, after *parser.TemplateFile) error`: returns nil, or a `*MismatchError`.
  - `type MismatchError struct { Before, After string }`, where `Before` and `After` are the first differing line of each normalized output, trimmed. `Error()` returns `rendered output differs: before %q, after %q`.

- [ ] **Step 1: Write the failing tests.** Each test parses both sides with `parser.ParseString`.
  - `TestEquivalentWhitespaceAtSoftBoundary`: `<p><b>a</b> <i>b</i></p>` vs `<p><b>a</b>⏎<i>b</i></p>` gives nil.
  - `TestMismatchAtGluedBoundary`: `<p><span>a</span><span>b</span></p>` vs `<p><span>a</span>⏎<span>b</span></p>` gives a `*MismatchError` whose `After` contains `</span> <span>`.
  - `TestEquivalentIgnoresPositions`: the same template with two extra blank lines before it gives nil.
  - `TestEquivalentIgnoresGoSpacing`: `{ a+b }` vs `{ a + b }` gives nil.
- [ ] **Step 2: Run** `go test ./internal/rendercheck/`. Expected: FAIL (package doesn't exist).
- [ ] **Step 3: Implement** `Render`, `Equivalent` and `MismatchError` as specified.
- [ ] **Step 4: Run** `go test ./internal/rendercheck/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(fmt): add render-equivalence oracle`.

---

### Task 2: Boundary census and shared classifier

**Files:**
- Create: `parser/v2/boundary.go`
- Create: `generator/boundary_census_test.go`
- Modify: `generator/generator.go:658-722`. Replace the `isTrailingSpaceNeeded` call site and its helpers `isInlineOrText`, `isTextLike` and `isSelfClosingTemplElementExpression` with `parser.RendersTrailingSpace`.
- Test: `parser/v2/boundary_test.go`

**Interfaces:**
- Consumes: `rendercheck.Render` (Task 1).
- Produces, in package `parser`:
  - `type Boundary int`, with constants `BoundaryIgnored`, `BoundaryGlued`, `BoundarySoft`, `BoundaryFixed`.
  - `func ClassifyBoundary(prev, next Node, ws TrailingSpace) Boundary`
    - `prev` and `next` are the non-`*Whitespace` nodes on either side of the gap.
    - `next` is the node after the enclosing statement when `prev` is the last node in an `if`/`for`/`switch` body.
    - `next` is nil at an element's closing edge or a template's end.
    - `ws` is the whitespace written in the gap. It is ignored for the result, except to pick glued (`SpaceNone`) versus soft at boundaries that render.
  - `func RendersTrailingSpace(current, next Node) bool`: exactly today's `isTrailingSpaceNeeded`, moved here. The generator calls it.

- [ ] **Step 1: Write the census test** `TestBoundaryCensus` in `generator/boundary_census_test.go`. It builds templates from a matrix and checks `ClassifyBoundary` against the rendered result.
  - **Node kinds:**
    - text `t`
    - string expression `{ s }`
    - inline element `<b>x</b>`
    - multi-line inline element `<a>⏎x⏎</a>`
    - block element `<div>x</div>`
    - void `<br/>`
    - self-closing call `@c()`
    - call with children `@c() {⏎x⏎}`
    - `if x {⏎<i>y</i>⏎}`
    - `for` and `switch` in the same shape as `if`
    - HTML comment `<!-- c -->`
    - Go comment `// c`, which is only valid at line start
    - raw Go `{{ y := 1 }}`
    - `{ children... }`
  - **Gaps:** `""`, `" "`, `"   "`, `"\n"`, `"\n\n"`.
  - **Contexts:** siblings inside `<p>…</p>`; at an element's opening and closing edges; and `statementBodyEnd`, where `prev` is the last child inside an `if` followed by each kind.
  - **Expected classification,** derived from `rendercheck.Render` of `prev+gap+next` for each gap:
    - All gaps render the same as `""`: `BoundaryIgnored`.
    - `" "`, `"   "`, `"\n"` and `"\n\n"` render the same, but differently from `""`: `BoundarySoft` (`BoundaryGlued` when the gap is `""`).
    - `" "` renders differently from `"\n"`: `BoundaryFixed`.
    - Skip any combination that fails to parse, and log it.
  - **Anchor assertions** that must hold, measured in the planning session:
    - `t` → `@c()` is fixed.
    - `{ s }` → `<a>⏎x⏎</a>` is glued with gap `""`.
    - `<b>x</b>` → `<b>x</b>` is soft.
    - `<!-- c -->` → `t` is ignored.
- [ ] **Step 2: Write** `TestClassifyBoundaryMatchesGenerator` in `parser/v2/boundary_test.go`. For every case in the old generator's logic, `RendersTrailingSpace` must equal the old `isTrailingSpaceNeeded` truth table. Copy the cases from `generator.go:665-721` into the test.
- [ ] **Step 3: Run** `go test ./parser/v2/ ./generator/ -run 'Census|ClassifyBoundary'`. Expected: FAIL (undefined).
- [ ] **Step 4: Implement** `ClassifyBoundary` and `RendersTrailingSpace` in `parser/v2/boundary.go`, then switch the generator to `parser.RendersTrailingSpace`. Text-internal spaces are not boundaries; Task 4 owns them. A text node whose `Value` ends in a space before a comment shows up in the census as ignored, because the space belongs to the text.
- [ ] **Step 5: Run** `go test ./...`. Expected: PASS, including every `generator/test-*` render test unchanged.
- [ ] **Step 6: Commit** `refactor: share whitespace boundary classification between generator and formatter`.

---

### Task 3: Document IR and printer

**Files:**
- Create: `internal/format/doc/doc.go`
- Create: `internal/format/doc/print.go`
- Test: `internal/format/doc/print_test.go`

**Interfaces:**
- Produces, in package `doc`:
  - `type Doc interface{ isDoc() }`
  - Constructors:
    - `Text(s string)`, where `s` contains no `\n`
    - `Line()`: a space when flat, a newline when broken
    - `SoftLine()`: nothing when flat, a newline when broken
    - `HardLine()`: always a newline, and it breaks every enclosing group
    - `BlankLine()`: a hard line followed by an empty line
    - `Group(docs ...Doc)`
    - `Indent(docs ...Doc)`
    - `Concat(docs ...Doc)`
    - `Fill(parts ...Doc)`: alternating content and separator; each separator breaks only if the next content doesn't fit
  - `const TabWidth = 4`
  - `func Print(d Doc, width int) string`: Prettier's algorithm. Indentation is `\t` per `Indent` level, counted as `TabWidth` columns. Lines never have trailing whitespace.

- [ ] **Step 1: Write the failing tests.**
  - `TestGroupFits`: `Group(Text("<p>"), Indent(SoftLine(), Text("hi")), SoftLine(), Text("</p>"))` at width 100 prints `<p>hi</p>`.
  - `TestGroupBreaks`: the same group at width 5 prints `<p>\n\thi\n</p>`.
  - `TestHardLinePropagates`: a group containing a `HardLine` always breaks.
  - `TestFill`: words `a b c d` with `Line()` separators at width 3 print `a b\nc d`.
  - `TestTabCountsAsTabWidth`: one indent level plus `Text("123456")` at width 9 breaks, because 4 + 6 > 9.
  - `TestNoTrailingWhitespace`: a broken `Line()` leaves no trailing space.
- [ ] **Step 2: Run** `go test ./internal/format/doc/`. Expected: FAIL.
- [ ] **Step 3: Implement** the constructors and `Print`, using the standard fits/print stack of `(indent, mode, doc)`.
- [ ] **Step 4: Run** `go test ./internal/format/doc/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(fmt): add document IR and width-aware printer`.

---

### Task 4: Template printer: elements, attributes, text, boundaries

**Files:**
- Create: `internal/format/printer.go`
- Create: `internal/format/printer_text.go` (text refill and line-start hazards)
- Test: `internal/format/printer_test.go`, plus new fixtures under `internal/format/testdata/` (txtar `-- in --` / `-- out --`, as today)

**Interfaces:**
- Consumes: `parser.ClassifyBoundary` (Task 2), `doc.*` (Task 3), `rendercheck.Equivalent` (Task 1).
- Produces:
  - `func Print(t *parser.TemplateFile, width int) ([]byte, error)`. Every top-level node except `*parser.HTMLTemplate` is written by its existing `Write(w, 0)`. Each `HTMLTemplate` writes its header as today, then a body built from `nodesDoc`, then `}`.
  - Unexported, used again in Task 5:
    - `func nodesDoc(nodes []parser.Node, after parser.Node) doc.Doc`
    - `func elementDoc(e *parser.Element, gluedBefore bool) doc.Doc`. When `gluedBefore` is true, the tag break goes inside this opening tag.

Layout rules for this task:
- **Boundary kinds:**
  - Soft: `Line()`, or `BlankLine()` when the source gap had two or more newlines.
  - Ignored: `SoftLine()` (`BlankLine()` as above).
  - Glued: nothing, and the next element gets `gluedBefore=true`.
  - Fixed: the original whitespace, emitted as `Text(" ")` or `HardLine()`.
- **Element without attributes:** `Group("<name", gluedBefore ? SoftLine() : nil, ">", Indent(SoftLine(), children), SoftLine(), "</name>")`.
- **Element with attributes:** `Group("<name", Indent(Line(), attr, Line(), attr…), SoftLine(), ">", …)`. When it breaks, each attribute goes on its own line and `>` goes on its own line. Attribute text comes from each attribute's existing `Write`.
- **Block elements and elements whose children contain a block node** always break their children.
- **Text:** a `Fill` of words split on single `' '` only. A run of two or more spaces, or any tab, joins its neighbours into one unbreakable `Text`. The separator before a line-start hazard word is `Text(" ")`, never `Line()`. Leading and trailing spaces in `Value` stay attached to the first and last word.
- **`<pre>`/`<textarea>`:** no special case.
- **Glued boundary with no following opening tag:** no break possible.

- [ ] **Step 1: Write the fixtures.** Each `-- out --` must also pass `rendercheck.Equivalent`.
  - `glued_inline_siblings_use_tag_break.txt`: `<p><span>a</span><span class="x">` + 120 characters of text + `</span></p>`. The output contains `</span><span⏎\t\t\tclass="x"⏎\t\t>`.
  - `soft_boundary_becomes_newline.txt`: a long `<p>` of inline siblings separated by spaces breaks one per line.
  - `text_refills_at_width.txt`: a 250-character sentence refills into lines of 100 columns or fewer.
  - `text_space_runs_are_unbreakable.txt`: `alpha   beta` stays together on one line.
  - `text_does_not_break_before_hazards.txt`: a long sentence containing ` if you`, ` for real`, ` @home` and ` // note`; no output line starts with `if `, `for `, `@` or `//`.
  - `glued_close_tag_text_overflows.txt`: `<p><a href="/">` + long text + `</a>.</p>`. `</a>.` stays together.
  - `attributes_break_one_per_line.txt`: an opening tag over 100 columns puts each attribute on its own line and `>` on its own line.
  - `pre_is_formatted_normally.txt`
  - `blank_lines_collapse_to_one.txt`
- [ ] **Step 2: Write** `TestPrint`. For each fixture, `Print` at width 100 must equal `out`, `rendercheck.Equivalent(parse(in), parse(out))` must return nil, and `Print(parse(out))` must equal `out`.
- [ ] **Step 3: Run** `go test ./internal/format/ -run TestPrint`. Expected: FAIL.
- [ ] **Step 4: Implement** `Print`, `nodesDoc`, `elementDoc` and the text refill for elements, text and string expressions. Other node kinds go to Task 5; until then, write them with their existing `Write` as a single `Text` per line.
- [ ] **Step 5: Run** `go test ./internal/format/ -run TestPrint`. Expected: PASS.
- [ ] **Step 6: Commit** `feat(fmt): print elements, attributes and text with render-safe reflow`.

---

### Task 5: Template printer: statements, calls, comments, script, raw Go

**Files:**
- Modify: `internal/format/printer.go`
- Test: new fixtures under `internal/format/testdata/`

**Interfaces:**
- Consumes: `nodesDoc` and `elementDoc` (Task 4).
- Produces: `nodesDoc` handles every `parser.Node` type in `generator.writeNode`'s switch.

Layout rules:
- **`if`/`for`/`switch`:** start on their own line with `HardLine()` before, unless the boundary before is glued; then they stay on the previous line. Bodies are indented and printed with `nodesDoc(body, nodeAfterStatement)`. Headers and Go expressions are gofmt'd with the same helpers today's `Write` methods use (`parser/v2/types.go` `format.Source` call sites), never width-broken.
- **`@call` and `@call { … }`:** same rules, with the boundary before decided by `ClassifyBoundary`. Fixed boundaries are reproduced exactly.
- **HTML comments, Go comments, `{{ }}` blocks, `<script>`, raw elements (`<style>`), doctype and `{ children... }`:** emitted with their existing `Write` output, re-indented, and surrounded by `HardLine()`. Their contents are never refilled.

- [ ] **Step 1: Write the fixtures.** Each must also pass `rendercheck.Equivalent`.
  - `statement_after_glued_element_stays_on_line.txt`: `<span>a</span>if x {` stays on one line.
  - `statement_body_last_child.txt`
  - `component_after_text_fixed_boundary.txt`: `text @c() more` and `text⏎@c()⏎more` both keep their form.
  - `text_before_comment_keeps_space.txt`
  - `script_and_style_unchanged_inside.txt`
  - `raw_go_block_reindented.txt`
- [ ] **Step 2: Run** `go test ./internal/format/ -run TestPrint`. Expected: FAIL on the new fixtures.
- [ ] **Step 3: Implement** the remaining node kinds in `nodesDoc`.
- [ ] **Step 4: Run** `go test ./internal/format/ -run TestPrint`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(fmt): print statements, calls, comments and embedded blocks`.

---

### Task 6: Wire the printer into `templ fmt` and the LSP with the self-check

**Files:**
- Modify: `internal/format/templ.go`
  - `Config` gains `Width int` (0 means 100).
  - `Templ` replaces `t.Write(w)` with `Print(t, width)`, then `rendercheck.Equivalent(t, reparsed)`.
- Modify: `cmd/templ/fmtcmd/main.go`: `Arguments.Width int`. The rest of the run already continues past per-file errors and exits non-zero.
- Modify: `cmd/templ/main.go`: `-width` flag for `fmt` and `lsp`, plus usage text.
- Modify: `cmd/templ/lspcmd/main.go`: `-width` flag passed through to `format.Config`.
- Modify: `cmd/templ/lspcmd/proxy/server.go:1110-1155`: replace `template.Write(w)` with `format.Print` plus `rendercheck.Equivalent`. On mismatch, log it and return no edits.
- Modify: `internal/format/format_test.go`
- Rewrite: all 66 `internal/format/testdata/*.txt` `-- out --` sections
- Test: `cmd/templ/fmtcmd/main_test.go`, `cmd/templ/lspcmd/lsp_test.go`

**Interfaces:**
- Consumes: `Print` (Tasks 4–5), `rendercheck.Equivalent` (Task 1).
- Produces:
  - `format.Templ(src []byte, fileName string, config Config) ([]byte, bool, error)`. Same signature as today. It wraps `*rendercheck.MismatchError` as `fmt.Errorf("%s: formatting would change rendered output: %w", fileName, err)`.
  - `format.Print(t *parser.TemplateFile, width int) ([]byte, error)`, exported from Task 4.

- [ ] **Step 1: Write the failing tests.**
  - `TestRunLeavesFileUntouchedOnRenderMismatch` in `cmd/templ/fmtcmd/main_test.go`:
    - Inject a printer that returns a different, valid template, through an unexported package variable `printTemplate = format.Print` in `internal/format`.
    - Assert the file on disk is byte-identical and `Run` returns an error containing `formatting would change rendered output`.
    - A second, valid file in the same directory is still formatted.
  - In `TestFormatting`: assert `rendercheck.Equivalent` between `in` and `out` for every fixture. Run the fixtures without Prettier when it's unavailable (`PrettierRequired: false`) and skip only the `attributes_*`/`*prettier*` fixtures.
  - Add an `-update` flag that rewrites the `-- out --` sections.
  - In the LSP test (`lsp_test.go:769-850`): expected output stays `format.Templ(...)`, which now uses the new printer.
- [ ] **Step 2: Run** `go test ./internal/format/ ./cmd/templ/...`. Expected: FAIL.
- [ ] **Step 3: Implement** the wiring and flags. Default width 100. Usage text: `-width` sets the maximum line width, default 100.
- [ ] **Step 4: Regenerate fixtures** with `go test ./internal/format/ -run TestFormatting -update`, then review every diff by hand.
  - Fixtures whose old expectation broke a glued boundary must now differ. For example, `br_elements_are_placed_on_new_lines.txt` keeps `Linebreaks<br/>and` glued.
  - Note any fixture whose new layout looks wrong, and fix the printer, not the fixture.
- [ ] **Step 5: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 6: Commit** `feat(fmt): use render-safe printer in templ fmt and LSP, with self-check and -width`.

---

### Task 7: Corpus and fuzz safety nets, and docs

**Files:**
- Create: `internal/format/corpus_test.go`
- Create: `internal/format/fuzz_test.go`
- Modify: `docs/docs/09-developer-tools/01-cli.md` ("Formatting templ files", around line 75)
- Modify: `docs/docs/09-developer-tools/02-ide-support.md` ("Formatting", around line 151)

**Interfaces:**
- Consumes: `format.Templ`, `rendercheck.Equivalent`.

- [ ] **Step 1: Write** `TestRepositoryTemplatesFormatSafely`. It walks `../../` for `*.templ`, including `generator/test-*` and `examples/`, and calls `format.Templ(src, path, Config{})` in memory, never writing to disk. Assert:
  - no error wraps `*rendercheck.MismatchError`
  - a second pass returns `changed == false`

  Log parse errors and skip those files.
- [ ] **Step 2: Write** `FuzzFormat`, seeded with every `-- in --` from `internal/format/testdata`. For inputs that parse, `format.Templ` must not return a `MismatchError`, and its output must be idempotent.
- [ ] **Step 3: Run** `go test ./internal/format/ -run TestRepositoryTemplatesFormatSafely` and `go test ./internal/format/ -fuzz FuzzFormat -fuzztime 5m`. Expected: PASS. Fix every printer bug found by adding a fixture to Task 4 or 5's set first.
- [ ] **Step 4: Update the docs.** Explain:
  - Formatting never changes what a template renders; `templ fmt` checks every file and refuses to write one that would render differently.
  - `><` across a line break (a tag break) means there is no space between the elements.
  - Use `{ "…" }` for literal whitespace in `<pre>` and `<textarea>`.
  - The `-width` flag.
- [ ] **Step 5: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 6: Commit** `test(fmt): corpus and fuzz render-safety checks; docs: describe safe formatting`.
