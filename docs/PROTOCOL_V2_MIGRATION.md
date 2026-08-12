# Protocol v2 migration note

Protocol v2 replaces the former flat JSON message format. Deploy the backend,
firmware, host simulator, and fixtures together with `protocol.Envelope` version 2.
The device WebSocket endpoint moves from `/v1/device` to `/v2/device`. A client that
sends version 1 receives `unsupported_protocol_version` and must upgrade; the project
does not dual-read or dual-write. If the coordinated change must be undone, revert it
in Git.
