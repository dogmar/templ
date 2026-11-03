# dprint-plugin-templ (Go Wasm) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the fork's formatter as a dprint Wasm plugin built from Go, after a spike proves that a Go `wasip1` reactor works as a dprint plugin.

**Architecture:**
- `cmd/dprint-plugin-templ` is a `GOOS=wasip1 GOARCH=wasm -buildmode=c-shared` reactor.
- It implements dprint's Wasm plugin schema v4 with `//go:wasmexport` functions over a shared byte buffer, and calls `internal/format`.
- Embedded `<script>`/`<style>` content goes back to dprint through the `host_format` import.

**Tech Stack:** Go 1.26 (`go:wasmexport`, wasip1 reactor), dprint ≥ 0.61 (0.64.3 is installed locally).

**Spec:** `docs/superpowers/specs/2026-11-02-render-equivalent-fmt-design.md`, section "dprint plugin". ABI reference: https://github.com/dprint/dprint/blob/main/docs/wasm-plugin-development.md

## Global Constraints

- **Plugin name:** `dprint-plugin-templ`. Config key: `templ`. File extension: `templ`.
- **`lineWidth`:** the plugin's own `lineWidth`, then dprint's global `lineWidth` if set, otherwise `100`.
- **Embedded content:** goimports is off. Prettier is never invoked. `<script>`/`<style>` content goes through `host_format`, and stays verbatim when the host returns no change.
- **Self-check failure:** returned as a format error whose text contains `formatting would change rendered output`.
- **Spike first:** Task 1's go/no-go is decided by the user from its recorded numbers. Don't start Task 2 without that decision.

## Review Focus

- **No plugin configured for `.js`/`.css` in dprint.** `host_format` returns an error or no change, and the content must stay verbatim, not fail the file. Pinned in Task 3's `TestHostFormatUnavailableLeavesScriptVerbatim`.
- **A file that doesn't parse.** dprint should show the templ parse error, and the plugin must not trap or panic. Pinned in Task 2's `parse_error.templ` case.
- **Many files formatted in one process.** Per-call state such as the shared buffer, file path and override config must not leak between files. Pinned in Task 2's `TestSequentialFormatsAreIndependent`.
- **Global `lineWidth: 120` with no plugin `lineWidth`.** The plugin uses 120, not 100. Pinned in Task 2's config test.
- **Already formatted input.** The plugin must report no change (dprint's "no change" result), not rewrite the file. Pinned in Task 2.

---

### Task 1: Spike: a Go wasip1 reactor dprint can load

**Files:**
- Create: `cmd/dprint-plugin-templ/main.go`
- Create: `cmd/dprint-plugin-templ/abi.go`: shared buffer and exports
- Create: `cmd/dprint-plugin-templ/build.sh`: `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .`
- Create: `cmd/dprint-plugin-templ/testdata/dprint.json` and `testdata/a.templ`
- Create: `cmd/dprint-plugin-templ/SPIKE.md`: the measurements

**Interfaces:**
- Produces: every export the ABI reference lists as required for schema v4:
  - `dprint_plugin_version_4`
  - `get_shared_bytes_ptr`, `clear_shared_bytes`
  - `register_config`, `get_config_diagnostics`, `get_resolved_config`, `get_config_file_matching`, `get_license_text`, `get_plugin_info`
  - `set_override_config`, `set_file_path`, `format`, `get_formatted_text`, `get_error_text`

  In the spike, `format` calls the current `format.Templ(src, path, format.Config{})`.

- [ ] **Step 1: Implement** the exports with `//go:wasmexport`. `main()` is empty. Keep per-config state in a `map[uint32]config`.
- [ ] **Step 2: Build** with `./build.sh`. Expected: `plugin.wasm` exists. Record its size in `SPIKE.md`.
- [ ] **Step 3: Run** `dprint fmt --config testdata/dprint.json --stdin a.templ < testdata/a.templ`, with `"plugins": ["./plugin.wasm"]`. Expected: formatted output on stdout.
- [ ] **Step 4: Record** in `SPIKE.md`:
  - the Wasm size
  - the cold `dprint fmt` time for one file
  - the warm time for 50 copies of `generator/test-*/template.templ`, compared with `templ fmt`
  - any ABI or WASI problems
- [ ] **Step 5: Commit** `spike: Go wasip1 dprint plugin`, then stop and present `SPIKE.md` to the user for go/no-go. If the answer is no-go, document `dprint-plugin-exec` with `templ fmt -stdin` in `docs/docs/09-developer-tools/01-cli.md` and end this plan.

---

### Task 2: Config resolution and formatting through the shared formatter

**Files:**
- Modify: `cmd/dprint-plugin-templ/main.go`, `abi.go`
- Create: `cmd/dprint-plugin-templ/config.go`
- Modify: `internal/format/templ.go`: `Config` gains `SkipImports bool` and `SkipPrettier bool`
- Test: `cmd/dprint-plugin-templ/config_test.go`, `plugin_test.go` (run on the host with plain `go test`, calling the Go functions behind the exports)

**Interfaces:**
- Consumes: `format.Templ` and `format.Config{Width, SkipImports, SkipPrettier}`. `Width` comes from the formatter plan's Task 6.
- Produces: `func resolveConfig(pluginCfg, globalCfg map[string]any) (resolved Config, diagnostics []Diagnostic)`, where `Config{LineWidth int}`.

- [ ] **Step 1: Write the failing tests.**
  - `TestResolveLineWidth`: `{}`/`{}` gives 100; `{}`/`{lineWidth:120}` gives 120; `{lineWidth:80}`/`{lineWidth:120}` gives 80; `{lineWidth:"x"}` gives a diagnostic.
  - `TestFormatNoChange`: formatted input returns the no-change result.
  - `TestParseErrorIsReturned`: `testdata/parse_error.templ` returns error text containing the parser message.
  - `TestSequentialFormatsAreIndependent`: two different files formatted in turn each produce their own expected output.
  - `TestSelfCheckFailureIsError`: the error text contains `formatting would change rendered output` (inject through `format`'s `printTemplate` seam).
- [ ] **Step 2: Run** `go test ./cmd/dprint-plugin-templ/`. Expected: FAIL.
- [ ] **Step 3: Implement** `resolveConfig`, the `SkipImports`/`SkipPrettier` options in `format.Templ`, and `format` calling `format.Templ` with `Width: resolved.LineWidth, SkipImports: true, SkipPrettier: true`.
- [ ] **Step 4: Run** `go test ./cmd/dprint-plugin-templ/ ./internal/format/`. Expected: PASS. Rebuild and repeat Task 1's step 3.
- [ ] **Step 5: Commit** `feat(dprint): config resolution and shared formatter`.

---

### Task 3: Embedded `<script>`/`<style>` through dprint host formatting

**Files:**
- Modify: `internal/format/templ.go`: `Config` gains `FormatEmbedded func(fileName string, src string) (string, error)`. When set, it's used for `<script>` (as `x.js`) and `<style>` (as `x.css`) contents in place of Prettier, reusing the depth calculation in `ApplyPrettier`.
- Create: `cmd/dprint-plugin-templ/host.go`: wraps the `dprint` module imports `host_write_buffer`, `host_format`, `host_get_formatted_text` and `host_get_error_text` with `//go:wasmimport`
- Test: `internal/format/embedded_test.go`, `cmd/dprint-plugin-templ/testdata/dprint-with-js.json`

**Interfaces:**
- Consumes: `format.Config` (Task 2).
- Produces: `func hostFormat(fileName, src string) (string, error)`, passed as `FormatEmbedded`.

- [ ] **Step 1: Write the failing tests.**
  - `TestFormatEmbeddedIsUsedForScriptAndStyle`: a fake `FormatEmbedded` that upper-cases its input changes only the script and style bodies.
  - `TestHostFormatUnavailableLeavesScriptVerbatim`: a fake that returns an error leaves the content unchanged, and the file still formats.
- [ ] **Step 2: Run** `go test ./internal/format/ -run Embedded`. Expected: FAIL.
- [ ] **Step 3: Implement** `FormatEmbedded` handling and `hostFormat`.
- [ ] **Step 4: Run** the unit tests, then `dprint fmt --config testdata/dprint-with-js.json` with dprint-plugin-typescript and malva configured. Expected: script and style contents are formatted by those plugins. Without them, the contents stay verbatim.
- [ ] **Step 5: Commit** `feat(dprint): format script and style via dprint host`.

---

### Task 4: Build and usage docs

**Files:**
- Modify: `cmd/dprint-plugin-templ/build.sh`: add `-trimpath -ldflags=-s` and print the size
- Modify: `docs/docs/09-developer-tools/01-cli.md`: a "dprint" subsection with a sample `dprint.json` and the config table (`lineWidth`)

- [ ] **Step 1: Write the docs** and a sample config, then check them by running the sample against `examples/` with `dprint check`. Expected: exit 0 after `dprint fmt`.
- [ ] **Step 2: Commit** `docs(dprint): document dprint-plugin-templ`.
