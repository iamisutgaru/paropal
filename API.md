# Daemon API Documentation

This document describes the HTTP API exposed by `daemon.go`.

## Overview

- Listen address: `:8080`
- Base URL (local): `http://localhost:8080`
- Response format: `application/json`
- Upstream provider: Vultr API (`https://api.vultr.com/v2`)

## Required Environment Variables

- `VULTR_API_KEY`: Bearer token used for Vultr API requests.
- `SHUTDOWN_BEARER_TOKEN`: Bearer token required for the shutdown endpoint.

If either variable is missing, the daemon exits at startup.

## Authentication

Only `POST /shutdown` is authenticated.

- Header: `Authorization: Bearer <token>`
- `<token>` must exactly match `SHUTDOWN_BEARER_TOKEN`.
- On auth failure, the daemon returns:
  - Status: `401 Unauthorized`
  - Header: `WWW-Authenticate: Bearer realm="daemon-shutdown"`
  - Body: `{"error":"unauthorized"}`

## Endpoints

### `GET /charges`

Returns pending account charges from Vultr.

#### Success

- Status: `200 OK`
- Body:

```json
{
  "pending_charges": 12.34
}
```

#### Errors

- `502 Bad Gateway`

```json
{
  "error": "failed to fetch pending charges from Vultr"
}
```

#### Example

```bash
curl -s http://localhost:8080/charges
```

### `GET /instance`

Returns the first Vultr instance whose label starts with `paropal-`.

#### Success

- Status: `200 OK`
- Body:

```json
{
  "status": "active",
  "ip": "203.0.113.10",
  "label": "paropal-prod-1"
}
```

#### Errors

- `404 Not Found`

```json
{
  "error": "no instance found with label prefix paropal-"
}
```

- `502 Bad Gateway`

```json
{
  "error": "failed to fetch instances from Vultr"
}
```

#### Example

```bash
curl -s http://localhost:8080/instance
```

### `POST /shutdown`

Triggers graceful server shutdown. Authentication required.

#### Request Headers

- `Authorization: Bearer <SHUTDOWN_BEARER_TOKEN>`

#### Success

- Status: `202 Accepted`
- Body:

```json
{
  "status": "shutting down"
}
```

The daemon then begins graceful shutdown with a 15 second timeout.

#### Errors

- `401 Unauthorized`

```json
{
  "error": "unauthorized"
}
```

- `500 Internal Server Error` (server state not initialized)

```json
{
  "error": "server is not initialized"
}
```

#### Example

```bash
curl -s -X POST \
  -H "Authorization: Bearer ${SHUTDOWN_BEARER_TOKEN}" \
  http://localhost:8080/shutdown
```

## Upstream Vultr Behavior

- Request timeout to Vultr: 10 seconds.
- Instance lookup calls `GET /instances?per_page=100` and follows cursor pagination.
- Non-2xx Vultr responses are treated as failures and mapped to API error responses above.
