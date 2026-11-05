# htmx (vendored)

- Package: `htmx.org` 4.0.1 from the npm registry (`dist/htmx.min.js`, `dist/ext/hx-csp.min.js`,
  `dist/ext/hx-sse.min.js`), unmodified, license in `LICENSE`.
- npm integrity of the tarball:
  `sha512-oF7JgXhDW9UXCy/k1ZYFUQgzu/rm19ORWPSPNL91vPluB2PB9+MV55tyQdH/w7o2EAbTYUG5SDW1R60mdkdqGg==`
- `SHA256SUMS` lists the files; a test fails when they change without it.

Update: download the new tarball, compare its sha512 with `dist.integrity` from
`https://registry.npmjs.org/htmx.org/<version>`, copy the three files, regenerate `SHA256SUMS`, update this file
and check the release notes for changes to `hx-csp`.
