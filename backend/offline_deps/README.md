# Offline E2E compatibility modules

These modules exist **only** for `make e2e-offline` in network-isolated CI/sandboxes.
They preserve the production import paths through `backend/go.offline.mod` while using:

- system `libsqlite3` through a small `database/sql` CGo driver,
- system `libopus.so.0` through a minimal CGo wrapper, and
- a minimal RFC6455 WebSocket client/server used only by the test process.

Production and release builds continue to use `backend/go.mod` (Go 1.25 and the pinned upstream modules). Do not ship these compatibility modules as application providers.
