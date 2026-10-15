# ADR 0001 — WebRTC DataChannels como transporte de administración

- Estado: Aceptado
- Contexto: se quiere todo en tiempo real, el mismo protocolo para la web y las apps nativas, y acceso remoto sin exponer HTTPS. El autor ya domina pion por go-link-desk.
- Decisión: toda la administración va por DataChannels (`rpc`, `events`, `metrics`, `files.N`, `term`). HTTP se limita a servir el bundle estático y a un WebSocket de señalización.
- Consecuencias: hay que construir RPC + eventos (`pkg/rtcrpc`) y un Service Worker para las URLs. El throughput del navegador se mide en M0 (gate). Para el volumen pesado sigue estando SMB.
