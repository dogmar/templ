# Spike: a Go wasip1 reactor as a dprint Wasm plugin

Date: 2026-11-02. Go 1.27.3 (darwin/arm64), dprint 0.64.3.

## Result

dprint loads the Go plugin, the schema v4 ABI works, and the plugin
formats templ files the same way `templ fmt` does, at about the same
speed once dprint has cached the compiled plugin. Two problems had to be
worked around. One is fixed in `build.sh`. The other is not fixed in the
committed code.

1. **Fixed in `build.sh`: dprint's monotonic clock reads 0 at first.**
   On a warm run (compiled plugin cached), every start failed:

   ```
   [dprint-plugin-templ]
   fatal error: nanotime returning zero
   ...
   Error initializing module: error while executing at wasm backtrace:
       0: 0x15501b - <unknown>!runtime.abort
       ...
       5:  0xc216e - <unknown>!runtime.main
       ...
       8: 0x1574e3 - <unknown>!_rt0_wasm_wasip1_lib: wasm trap: wasm `unreachable` instruction executed
   ```

   dprint's sandboxed `clock_time_get(CLOCK_MONOTONIC)` returns the time
   since its first call, rounded down to a millisecond
   (`crates/dprint/src/plugins/implementations/wasm/instance/wasi.rs`,
   `clock_now_nanos`: `MONOTONIC_START.get_or_init(Instant::now).elapsed()`).
   Go's runtime calls `nanotime()` in `runtime.main`, before any user code
   runs, and throws if it reads 0. A warm start takes less than 1 ms, so it
   always reads 0 there. Cold runs worked, but they printed `Compiling ...`
   twice and took about 1.6 s, so the first instance probably failed the
   same way and was retried.

   Workaround: `build.sh` builds with `-overlay`, replacing
   `runtime/os_wasip1.go` with a copy whose `nanotime1` returns
   `int64(time) + 1`. A 1 ns offset on the monotonic clock does no harm.
   This relies on the text of the Go runtime source, so `build.sh` fails
   if it can't find the line to change. The real fix belongs in dprint:
   start the monotonic clock above zero. That's a small upstream issue.

2. **Not fixed: goimports can't run under WASI.** `format.Templ` calls
   `imports.Process`, and `golang.org/x/tools/imports.Process` always
   runs `go env` to set up its environment. The public API gives no way
   to pass that environment in. Under wasip1 that fails:

   ```
   failed to get imports from generated go code: failed to process go code "...": err: pipe: Not implemented on wasip1: stderr:
   ```

   So with the code as committed, the plugin returns this error for every
   file (dprint exits non-zero). Even with the environment supplied, the
   module and GOPATH resolvers would need `go list` and file system
   access, and the sandbox has neither. Only fixes from the compiled-in
   stdlib table could work.

   For the measurements below I temporarily changed
   `internal/imports/process.go` (not committed) to pass
   `&imports.Options{FormatOnly: true, Comments: true, TabIndent: true, TabWidth: 8}`.
   That keeps gofmt-style import formatting but stops imports being added
   or removed. On the 50 generator templates the output was byte-for-byte
   the same as `templ fmt` with real goimports, because those files
   already have the right imports.

Prettier: the plugin passes `PrettierCommand: "templ-dprint-no-prettier"`.
`exec.LookPath` fails under wasip1, so Prettier is skipped as intended
with no error.

## Measurements

| What | Result |
| --- | --- |
| `plugin.wasm` size | 11,776,184 bytes (11.78 MB, 11.23 MiB) |
| Cold, 1 file (`dprint clear-cache`, then `dprint fmt --stdin`) | 0.79 to 0.83 s real (4 runs). Most of it is compiling the Wasm (about 5.9 s user across threads) |
| Warm, 1 file (`--stdin`) | 0.01 s real |
| Warm, 50 files, dprint (2nd+ run, `incremental: false`) | 0.25 to 0.27 s real, 1.6 s user (3 runs) |
| Same 50 files, `templ fmt` (host binary, real goimports) | 0.30 to 0.33 s real, 0.5 s user (3 runs) |
| Same 50 files, `templ fmt` (host binary, FormatOnly patch) | 0.24 to 0.25 s real (3 runs) |

The 50 files are `generator/test-*/template.templ` (the first 50 of 54,
copied to `/tmp/dprint-spike/NN.templ`). 22 of them change. The files
were restored from a clean copy before every timed run, and dprint ran
once beforehand so the compiled plugin was cached. The dprint and
`templ fmt` output trees were identical. The dprint timings use the
FormatOnly patch (problem 2).

Commands:

```sh
./build.sh
dprint clear-cache
time dprint fmt --config testdata/dprint.json --stdin a.templ < testdata/a.templ
# 50 files: /tmp/dprint-spike/dprint.json is
# {"templ": {}, "incremental": false, "plugins": ["<abs path>/plugin.wasm"]}
time dprint fmt --config /tmp/dprint-spike/dprint.json
time templ fmt /tmp/dprint-spike-copy
```

Plugin paths in a dprint config file resolve relative to that file, so
`testdata/dprint.json` uses `../plugin.wasm`. The test input is
`testdata/a.templ.txt`, not `a.templ`, because the repository's
`templ fmt .` (CI `ensure-fmt`) would otherwise format it and fail the
clean check. `--stdin a.templ` supplies the file name.

## ABI notes

- `//go:wasmexport` with `-buildmode=c-shared` produces a reactor that
  exports `_initialize` and `memory`. dprint 0.64.3 accepted it and found
  every schema v4 export.
- Every pointer and length is a `uint32`. The shared buffer is a Go
  `[]byte` global. Go's GC doesn't move objects, so a pointer to its
  backing array stays valid while the global holds it. Buffers are given
  a capacity of at least 1 so a zero-length buffer still has an address.
- Go's runtime doesn't need any WASI calls beyond what dprint provides:
  clocks, `random_get`, `fd_write`, `poll_oneoff`, plus stubs for the
  rest.
- Each instance starts a Go runtime. dprint runs several instances in
  parallel, which is why user time (1.6 s) is well above real time for
  the 50-file run.

## Recommendation: go, with two conditions

Go: the ABI works, the plugin is a single portable file with no
dependencies, warm throughput matches `templ fmt`, and the output is
identical. The 11.8 MB size and 0.8 s first-run compile are paid once
per plugin version, because dprint caches the compiled module.

Conditions:

1. **Clock**: ship the `build.sh` runtime overlay for now, and file an
   issue on dprint to start `CLOCK_MONOTONIC` above zero. Without one of
   the two, the plugin can't start once it's cached.
2. **goimports**: decide what the plugin does with imports under WASI.
   Options: (a) FormatOnly under wasip1, so missing or unused imports are
   left alone and output can differ from `templ fmt` on such files;
   (b) skip import processing entirely under wasip1; (c) write a
   stdlib-only import fixer from x/tools' compiled-in symbol table, which
   is more work and still can't resolve module imports. (a) is the
   smallest change and matches `templ fmt` whenever imports are already
   correct.
