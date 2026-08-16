# Asset-bundle font test fixtures

These fixtures are test corpus only; they are not a Product-v1 font selection.

- Upstream: Noto Sans Regular, version 2.004, from `notofonts/latin-greek-cyrillic`.
- Upstream TTF SHA-256 used to make the subsets: `89c3c497f618fdaa0b2d1e98fef93582f28c71debd2c4a8cdf41f190ced2909d`.
- License: SIL Open Font License 1.1; the exact upstream `OFL.txt` is stored beside these fixtures.
- `NotoSans-EN-subset.ttf.gz`: printable-ASCII test subset; gzip SHA-256 `25e0b899a5c96620d71c9dbf787400119b40d3a204a997fc948972dce60032c0`.
- `NotoSans-VI-EN-subset.ttf.gz`: printable-ASCII plus the precomposed Vietnamese glyph profile used by the validator; gzip SHA-256 `74e3d7fc9facca949dd55333201b88e7b29918fff76fed9e61b92551fa820695`.

The repository stores deterministic gzip wrappers only to keep the fixture footprint small. Tests inflate the bytes and place the resulting real TTF payload into the signed asset bundle; schema v1 itself does not accept gzip font assets.
