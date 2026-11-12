# Third-party notices

Standalone builds embed the installed dependency license texts, available with
`gitbuddy --licenses` and in the accompanying `THIRD_PARTY_NOTICES.txt` asset.
The inventory includes build-only packages conservatively. It is not legal
clearance for redistribution.

The embedded Bun 1.4.4 runtime has its own MIT and third-party licenses, including
LGPL JavaScriptCore/WebKit; see its [license inventory and rebuild instructions](https://github.com/oven-sh/bun/blob/bun-v1.4.4/LICENSE.md).
OpenTUI 0.8.1's native library statically links LGPL-2.1-or-later FFmpeg 9.0.2;
its `LICENSE-FFMPEG` and `FFMPEG-SOURCE.md` are included in the generated inventory.
Preserve the corresponding upstream source/rebuild materials when redistributing.
Release preparation must assess these obligations; checksums and tests do not
establish license compliance.

## Bundled OpenCode themes and spinner

Gitbuddy bundles the OpenCode, Tokyo Night, and Catppuccin theme documents from
OpenCode **v2.2.7**. Only JSON whitespace has been changed; theme values are
unchanged. Legacy documents are migrated at runtime by `@opencode/theme/tui`.
The theme assets do not bundle OpenCode context providers. `src/spinner.tsx`
also adapts OpenCode v2.2.7's ten braille frames and renderable animation logic,
using OpenTUI's shared animation clock. The adaptation fixes the glyph width and
frame interval (one cell, 80 ms), accepts the caller's theme color, and omits
upstream custom frame/generator support. No private OpenCode application imports
are required.

| Local file | Upstream source |
| --- | --- |
| `src/themes/opencode.json` | https://raw.githubusercontent.com/anomalyco/opencode/v2.2.7/packages/tui/src/theme/assets/v2/opencode.json |
| `src/themes/tokyonight.json` | https://raw.githubusercontent.com/anomalyco/opencode/v2.2.7/packages/tui/src/theme/assets/tokyonight.json |
| `src/themes/catppuccin.json` | https://raw.githubusercontent.com/anomalyco/opencode/v2.2.7/packages/tui/src/theme/assets/catppuccin.json |
| `src/spinner.tsx` (frames) | https://raw.githubusercontent.com/anomalyco/opencode/v2.2.7/packages/tui/src/component/spinner-frames.ts |
| `src/spinner.tsx` (renderable adaptation) | https://raw.githubusercontent.com/anomalyco/opencode/v2.2.7/packages/tui/src/component/spinner-renderable.ts |

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
