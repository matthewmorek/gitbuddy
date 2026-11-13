# Third-party notices

Gitbuddy's original code is MIT-licensed, copyright (c) 2026 matthewmorek; see
`LICENSE`. Third-party code, assets and runtime components retain their own
licenses.

Standalone Go builds embed the resolved Go dependency license texts, available with
`gitbuddy --licenses` and in the accompanying `THIRD_PARTY_NOTICES.txt` asset.
The inventory includes module-level notices conservatively and the Go runtime
license. Preserve the notices when redistributing.

The application uses Bubble Tea v2, Lip Gloss v2 and Bubbles v2 under their MIT
licenses, plus the transitive Go modules recorded in `go.mod`/`go.sum` and the
generated inventory. The Go executable does not embed the former Bun/OpenTUI
runtime, JavaScriptCore, TinyCC or FFmpeg. Old development binaries are not part
of the Go release bundle.

## Bundled OpenCode palettes

Gitbuddy adapts the OpenCode, Tokyo Night and Catppuccin palettes from OpenCode
**v2.2.7** into seven light/dark color roles. This is not the original theme schema
or parser: active surfaces are selected to remain darker than inactive panels.
No OpenCode context providers, TypeScript theme engine or spinner implementation
are included. Animation comes from Bubbles.

| Local file | Upstream source |
| --- | --- |
| `internal/palette/themes/opencode.json` | https://raw.githubusercontent.com/anomalyco/opencode/v2.2.7/packages/tui/src/theme/assets/v2/opencode.json |
| `internal/palette/themes/tokyonight.json` | https://raw.githubusercontent.com/anomalyco/opencode/v2.2.7/packages/tui/src/theme/assets/tokyonight.json |
| `internal/palette/themes/catppuccin.json` | https://raw.githubusercontent.com/anomalyco/opencode/v2.2.7/packages/tui/src/theme/assets/catppuccin.json |

Upstream license: https://raw.githubusercontent.com/anomalyco/opencode/v2.2.7/LICENSE

```text
MIT License

Copyright (c) 2025 opencode

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
