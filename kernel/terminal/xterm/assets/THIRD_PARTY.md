# Third-Party Assets

This directory contains vendored third-party JavaScript libraries used by the
xterm.js terminal renderer.

## xterm.js

- Files: `xterm.min.js`, `xterm.min.css`, `addon-fit.min.js`, `addon-unicode11.min.js`
- License: MIT
- Source: https://github.com/xtermjs/xterm.js
- Why vendored: avoid runtime CDN fetch (local-only deployment, offline-friendly).

The MIT license requires preservation of the copyright notice. See the upstream
project's LICENSE for the full text:
https://github.com/xtermjs/xterm.js/blob/master/LICENSE
