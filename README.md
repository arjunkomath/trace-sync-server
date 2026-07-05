# Trace Sync Server

Tiny self-hosted sync server for Trace settings.

It stores one plain JSON state file on disk and protects it with one shared bearer token. There are no accounts, database, encryption, pairing flow, web UI, or managed service assumptions.

## API

- `GET /health`
- `GET /v1/settings`
- `PUT /v1/settings`

`GET` and `PUT` require:

```http
Authorization: Bearer <TRACE_SYNC_TOKEN>
```

## Configuration

| Environment variable | Required | Default | Description |
| --- | --- | --- | --- |
| `TRACE_SYNC_TOKEN` | Yes | | Shared bearer token |
| `TRACE_SYNC_DATA_DIR` | No | `./data` | Directory for `state.json` |
| `TRACE_SYNC_PORT` | No | `8787` | HTTP port |
| `TRACE_SYNC_MAX_BYTES` | No | `1048576` | Max request body size |

## Run locally

```bash
TRACE_SYNC_TOKEN="$(openssl rand -hex 32)" go run .
```

Upload initial settings:

```bash
curl -X PUT http://localhost:8787/v1/settings \
  -H "Authorization: Bearer $TRACE_SYNC_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"baseVersion":0,"updatedBy":"MacBook Pro","settings":{"example":true}}'
```

Download settings:

```bash
curl http://localhost:8787/v1/settings \
  -H "Authorization: Bearer $TRACE_SYNC_TOKEN"
```

## Docker

```bash
docker build -t trace-sync-server .
docker run --rm -p 8787:8787 \
  --user "$(id -u):$(id -g)" \
  -v "$(pwd)/data:/data" \
  -e TRACE_SYNC_TOKEN="$(openssl rand -hex 32)" \
  trace-sync-server
```

## E2E test

The E2E script builds the Docker image, runs a local container, exercises auth, empty state, upload, download, conflict handling, and verifies the mounted JSON state file was written.

```bash
./e2e.sh
```

Optional overrides:

```bash
PORT=18788 IMAGE=trace-sync-server:e2e ./e2e.sh
```

## Docker Compose

```yaml
services:
  trace-sync:
    image: trace-sync-server
    user: "1000:1000"
    ports:
      - "8787:8787"
    volumes:
      - ./data:/data
    environment:
      TRACE_SYNC_TOKEN: "change-this-to-a-long-random-token"
      TRACE_SYNC_DATA_DIR: "/data"
```

## Conflict model

The server keeps a monotonically increasing version number.

Clients upload with the version they last saw:

```json
{
  "baseVersion": 7,
  "updatedBy": "MacBook Pro",
  "settings": {}
}
```

If the server is still on version `7`, the upload becomes version `8`.

If another client already wrote version `8`, the server returns `409 Conflict`:

```json
{
  "error": "conflict",
  "currentVersion": 8
}
```

Trace should then ask the user to download remote settings, overwrite remote settings, or cancel.

## Security model

This server stores Trace settings as plaintext JSON on disk. Keep the server private, use a strong token, and put it behind HTTPS when exposing it over a network.
