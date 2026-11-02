# qr-gen

Generador de códigos QR gratuito. Un solo binario en Go, sin base de datos, sin registro.

- **Web:** https://qr.hlgcodes.com (pendiente de despliegue)
- **API:** `GET /api/qr?data=...&size=512&fg=%23000000&bg=%23ffffff&level=M&format=png`
- **Health:** `GET /health`

## Desarrollo

```bash
go run .
# abre http://localhost:8080
```

## Build

```bash
go build -o qr-gen .
./qr-gen   # escucha en :8080 (o $PORT)
```

## Docker

```bash
docker build -t qr-gen .
docker run -p 8080:8080 qr-gen
```

## Despliegue (Dokploy)

1. Crear aplicación en Dokploy apuntando a `HlgCodes/qr-gen`, rama `main`, build type `Dockerfile`.
2. Dominio: `qr.hlgcodes.com` (Traefik gestiona TLS).
3. Puerto del contenedor: `8080`.
4. Auto-deploy en push a `main`.
5. Crear monitor en Kuma (tag `Dokploy=Service`, notificación a Telegram).

## Stack

Go 1.26, `skip2/go-qrcode`, HTML embebido con Tailwind (CDN). Sin dependencias externas en runtime. ~10 MB de binario, ~15 MB de RAM por instancia.
EOF