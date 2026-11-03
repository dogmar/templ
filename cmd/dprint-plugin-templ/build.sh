#!/bin/sh
# Builds plugin.wasm, a wasip1 reactor dprint loads as a Wasm plugin.
set -e
cd "$(dirname "$0")"

# dprint's monotonic clock counts from its first use, to the millisecond, so
# it reads 0 for the first millisecond. Go's runtime throws "nanotime
# returning zero" when it reads 0 during start up, so build against a copy of
# the runtime whose monotonic clock is offset by one nanosecond.
overlay=$(mktemp -d)
trap 'rm -rf "$overlay"' EXIT
runtime_file="$(go env GOROOT)/src/runtime/os_wasip1.go"
awk '
	/^func nanotime1\(\) int64 \{/ { in_nanotime = 1 }
	in_nanotime && /return int64\(time\)$/ { sub(/return int64\(time\)$/, "return int64(time) + 1"); patched = 1; in_nanotime = 0 }
	{ print }
	END { if (!patched) { print "build.sh: nanotime1 not found in " FILENAME > "/dev/stderr"; exit 1 } }
' "$runtime_file" > "$overlay/os_wasip1.go"
printf '{"Replace":{"%s":"%s"}}' "$runtime_file" "$overlay/os_wasip1.go" > "$overlay/overlay.json"

GOOS=wasip1 GOARCH=wasm go build -overlay "$overlay/overlay.json" -buildmode=c-shared -o plugin.wasm .
