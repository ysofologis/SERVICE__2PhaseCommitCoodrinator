# Two-Phase Commit API — How To Use

## Quick Start

### Prerequisites

- Go 1.21+
- SQLite (bundled as `modernc.org/sqlite` — no system dependency needed)
- Two participant services running at `localhost:8081` and `localhost:8082`

### Building

```bash
cd /storage/drive-S/Work/SideProjects/2PhaseCommitAPI

# Build both binaries
CGO_ENABLED=0 go build -o coor ./cmd/coordinator
CGO_ENABLED=0 go build -o part ./cmd/participant

# Or use Makefile
make build
```

### Running the Coordinator

```bash
# Default config: port 8080, SQLite at txn.db
./coor

# Custom port
PORT=:9090 ./coor

# Custom DB path
TXNDB_PATH=/tmp/txn.db ./coor
```

### Running a Participant Service

```bash
# Participant default port 8081
./part

# Custom port
PORT=:8083 ./part
```

## API Usage

### Create a Transaction (Async Mode — Default)

Returns `202 Accepted` immediately; poll for status.

**Request:**
```http
POST /transactions
Content-Type: application/json

{
  "topic_key": "order_123",
  "timeout_ms": 10000,
  "operations": [
    {
      "participant": "accounts-svc",
      "action": "create",
      "params": {"order_id": "ORD-1", "amount": 500}
    },
    {
      "participant": "inventory-svc",
      "action": "reserve",
      "params": {"item": "WIDGET", "qty": 2}
    }
  ]
}
```

**Response (202):**
```json
{
  "id": "tx_order_123-a1b2c3d4",
  "status": "INIT",
  "topic_key": "order_123",
  "message": "Transaction accepted, poll /transactions/{id} for status"
}
```

### Create a Transaction (Sync Mode)

Blocking — waits until terminal state or timeout. Returns `200` on commit, `408` on timeout, `422` on abort.

**Request:**
```http
POST /transactions?sync=true
Content-Type: application/json

{ ...same body as above... }
```

### Get Transaction Status

```http
GET /transactions/tx_order_123-a1b2c3d4
```

**Response (200):**
```json
{
  "id": "tx_order_123-a1b2c3d4",
  "topic_key": "order_123",
  "status": "COMMITTED",
  "created_at": "2026-09-16T20:00:00Z",
  "updated_at": "2026-09-16T20:00:02Z",
  "decision_at": "2026-09-16T20:00:02Z",
  "decided_by": "protocol",
  "participants": [
    {
      "name": "accounts-svc",
      "vote": "yes",
      "final": "committed",
      "prepared_at": "2026-09-16T20:00:01Z",
      "committed_at": "2026-09-16T20:00:02Z"
    },
    {
      "name": "inventory-svc",
      "vote": "yes",
      "final": "committed",
      "prepared_at": "2026-09-16T20:00:01Z",
      "committed_at": "2026-09-16T20:00:02Z"
    }
  ]
}
```

### List Transactions

```http
GET /transactions?limit=20&offset=0
```

### Add Participant Mid-Transaction

Adds a participant while the transaction is still in `PREPARING` state. If the new participant votes "no", the entire transaction aborts.

```http
POST /transactions/tx_order_123-a1b2c3d4/participants
Content-Type: application/json

{
  "participant": "shipping-svc",
  "action": "reserve",
  "params": {"item": "WIDGET"}
}
```

### Cancel a Transaction

```http
POST /transactions/tx_order_123-a1b2c3d4/cancel
```

Response `202 Accepted`. The transaction transitions to `ABORTED`.

### Health Check Endpoints

```http
GET /healthz   # Always returns 200 {"status":"ok"}
GET /readyz    # Returns 200 if DB is responsive, 503 if not
```

### Metrics

```http
GET /metrics   # Prometheus format
```

## Web UI (HTMX)

The coordinator serves an HTMX-powered dashboard at the root path.

| Route | Description |
|-------|-------------|
| `/` | Dashboard — lists recent transactions |
| `/new` | New transaction form |
| `/tx/{id}` | Transaction detail view |

The dashboard uses HTMX requests (`HX-Request: true`) to dynamically update the transaction list without full page reloads.

### Workflow Example

1. Open `http://localhost:8080/` → see transaction list
2. Click "New Transaction" → opens `/new` form
3. Fill in topic key, select participants and actions, submit
4. Form POSTs to `/transactions` → receives `202` with tx ID
5. HTMX polls `/tx/{id}` every 2 seconds until terminal state
6. Detail view updates with final result (COMMITTED/ABORTED)

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `:8080` | HTTP listen address |
| `TXNDB_PATH` | `txn.db` | SQLite database file |
| `SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown timeout |

## Testing

```bash
# Run all tests
go test ./...

# Run only integration tests
go test ./internal/coord -run Integration -v

# Build static binary (no CGO dependencies)
CGO_ENABLED=0 go build -ldflags='-w -s' ./cmd/coordinator
```

## Recovery on Startup

When the coordinator starts, it automatically re-drives any non-terminal transactions from the previous run:

1. Queries SQLite for transactions not in terminal state (COMMITTED/ABORTED/FAILED)
2. Queries each participant's `/tx/{id}` endpoint to determine their state
3. Decision logic:
   - Any participant aborted/voted no → sends abort to all
   - All committed → marks transaction COMMITTED
   - All prepared, none aborted → attempts to commit remaining
   - Mixed/uncertain → marks FAILED

This ensures durability: transactions persist across coordinator restarts.

## Error Codes

| HTTP Status | Meaning |
|-------------|---------|
| 200 OK | Success (sync mode returned result) |
| 202 Accepted | Transaction started asynchronously |
| 400 Bad Request | Invalid request/body |
| 404 Not Found | Transaction/participant not found |
| 408 Request Timeout | Sync mode timed out waiting |
| 409 Conflict | Topic key conflict or invalid state |
| 422 Unprocessable Entity | Transaction aborted (one participant voted no) |
| 500 Internal Server Error | Unexpected failure |

## Architecture Overview

```
Client ──┬── POST /transactions ──► Coordinator ──► POST /prepare ──► Participant A
         │                          │                    POST /prepare ──► Participant B
         │                          │
         │                     GET /tx/{id} (poll) ◄──── Coordinator
         │
         ├── POST /tx/{id}/participants ──► AddParticipant
         ├── POST /tx/{id}/cancel ──► Abort
         └── GET /tx/{id} ──► GetStatus
```

See `BACKEND.md` for detailed architecture documentation.
