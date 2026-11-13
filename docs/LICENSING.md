# Licensing the Go distribution

Gitbuddy's original code is MIT-licensed, copyright (c) 2026 matthewmorek.
The release executable is built from Go with `CGO_ENABLED=0`; it includes the Go
runtime, the Go modules recorded in `go.mod`/`go.sum`, and bundled palette data.
It does **not** contain the former Bun/OpenTUI runtime or its JavaScriptCore,
TinyCC and FFmpeg dependencies. Their source/relink packaging is therefore not
part of the Go release process. This does not change obligations for any older
Bun-based executable someone separately redistributes.

## Notices that travel with the executable

`python3 scripts/build.py --release` collects the root MIT license, the
[palette attribution](../THIRD_PARTY_NOTICES.md), Go's runtime license and the
resolved modules' license/notice files from the local toolchain/module cache.
The deterministic inventory is embedded as `internal/buildinfo/notices.txt`,
printed by `gitbuddy --licenses`, and copied to the release asset
`THIRD_PARTY_NOTICES.txt`. Module-level notices may cover unused code too.

The three main UI libraries—Bubble Tea, Lip Gloss and Bubbles—use MIT licenses.
Other module and runtime terms remain in the inventory rather than being
relicensed as Gitbuddy code. The flattened OpenCode palettes retain their full
upstream MIT notice and version-specific provenance.

## When changing dependencies

Review new module and runtime licenses before distribution, regenerate the
inventory, and keep it with the executable. Build/test success is not legal
certification. No bulk upstream source archive, native-library rebuild or bespoke
upstream modification manual is required by this Go project's release tooling.
Do not mix old development binaries into the exact four-file Go release bundle.
