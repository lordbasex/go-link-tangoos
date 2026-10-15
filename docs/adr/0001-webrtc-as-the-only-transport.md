# ADR 0001 — WebRTC DataChannels as the management transport

- Status: Accepted
- Context: we want everything in real time, the same protocol for the web and the native apps, and remote access without exposing HTTPS. The author already knows pion well from go-link-desk.
- Decision: all management goes over DataChannels (`rpc`, `events`, `metrics`, `files.N`, `term`). HTTP is limited to serving the static bundle and a signaling WebSocket.
- Consequences: we have to build RPC + events (`pkg/rtcrpc`) and a Service Worker for URLs. Browser throughput is measured in M0 (gate). SMB remains the path for heavy data volume.
