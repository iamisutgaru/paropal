# Daemon API Documentation

This document describes the HTTP API exposed by the `daemon` binary, plus its scheduled background behaviors.

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

Returns a Vultr instance whose label starts with `paropal-`.

If multiple instances match, the daemon selects a "best" candidate:

- Prefer an instance with a non-empty `main_ip`.
- Then prefer the lexicographically latest label (labels are timestamped).

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
- Instance creation calls `POST /instances` with hardcoded specs (see "Scheduled Provision Behavior").
- Block storage attachment calls `POST /blocks/{block_id}/attach` (see "Scheduled Provision Behavior").
- Non-2xx Vultr responses are treated as failures and mapped to API error responses above.

## Scheduled Cleanup Behavior

- The daemon runs a scheduled "destroy all instances" reconciliation at `00:10` in `Asia/Seoul` (KST).
- Cleanup is only allowed within the window `00:00 <= time < 07:00` KST.
- A hard cutoff at `07:00` KST stops further list/delete/retry operations for that day's run.
- While inside the window, cleanup retries until no instances remain or the cutoff is reached.

⚠️ Cleanup is account-wide: it deletes all instances in the Vultr account (not just `paropal-*`).

## Scheduled Provision Behavior

- The daemon runs a scheduled "ensure paropal instance exists" reconciliation at `07:10` in `Asia/Seoul` (KST).
- Catch-up behavior: if the daemon starts after `07:10` KST, it runs one provision pass immediately.
- If any `paropal-*` instance exists (and is not obviously terminating), creation is skipped.
- If the only `paropal-*` instance is in a terminating state (status contains `destroy`, `delete`, `terminate`, or `remove`), it is ignored and creation proceeds.

### Hardcoded Create Specs

These values are currently hardcoded to match `create.sh`:

- Region: `nrt`
- OS: Debian 13 (`os_id=2625`)
- Plan: `vhp-2c-2gb-amd`
- `user_scheme=limited` (Vultr provides a limited user `linuxuser`)
- `sshkey_id=["c426659e-454e-40de-8a8b-6b9820fe72f2"]`
- Label prefix: `paropal-` with timestamp in `Asia/Tokyo`, format `MM-DD_HH-MM-SS`

### Cloud-Init User Data

The daemon base64-encodes a cloud-init YAML document into Vultr `user_data`. It:

- Sets timezone `Asia/Tokyo` and locale `en_US.UTF-8`.
- Applies a "base init" immediately (via `runcmd`) to enforce:
  - SSH only on port `443`
  - `PermitRootLogin no`
  - `AllowUsers linuxuser`
  - No password auth
  - `AuthenticationMethods publickey keyboard-interactive` (key OR TOTP)
  - UFW allows only `443/tcp`, deny incoming otherwise
  - fail2ban enabled for sshd on port `443`
- Starts a systemd timer that retries block/dev initialization once per minute until it succeeds.

### Block Storage + Dev Initialization

After instance creation, the daemon attaches block storage:

- Block storage id: `52cb7c3a-42fd-47e1-b120-6e8cf6b2ddd1`
- Attach: `live=false`

Inside the instance, the retrying init waits for `/dev/vdb1`, then:

- Mounts it at `/mnt/blockstorage` (persisted via `/etc/fstab`).
- Copies secrets/credentials from the mounted volume into `/home/linuxuser/...`:
  - `linuxuser.google_authenticator` -> `/home/linuxuser/.google_authenticator` (mode `0400`)
  - `codex_auth.json` -> `/home/linuxuser/.codex/auth.json`
  - `gh_hosts.yml` -> `/home/linuxuser/.config/gh/hosts.yml`
  - `config`, `id_ed25519`, `id_ed25519.pub` -> `/home/linuxuser/.ssh/...`
- Installs dev conveniences (after block init succeeds): `git`, `ripgrep`, `fd`, `tmux` (with `set -g mouse on`), Node.js 22, Go 1.26.x, `gh`, `uv`, and `codex`.

### Provision Retry Behavior

- The provision reconciler retries on failures with exponential backoff (15s doubling up to 5m).
- Within a single scheduled run, once instance creation succeeds, retries will only retry block attachment (to avoid accidental double-creates during API lag).
