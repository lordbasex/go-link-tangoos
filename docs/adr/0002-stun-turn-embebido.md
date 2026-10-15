# ADR 0002 — STUN/TURN y señalización embebidos en tangod

- Estado: Aceptado
- Contexto: el producto es LAN-first. No debe depender de servidores externos (Google STUN, signalhub en la nube) para funcionar.
- Decisión: `tangod` sirve `/signal` (sobre JSON de signalhub), un STUN/TURN con `pion/turn` en 3478 con credenciales efímeras, e ICE en un único puerto UDP/TCP (8443) con mDNS habilitado.
- Consecuencias: funciona sin Internet. TangoConnect (remoto) se agrega luego como señalización federada a signalhub + coturn.
