# 2-Phase Commit Web API

A Go implementation of the Two-Phase Commit (2PC) protocol with an HTTP API and HTMX frontend.

## Features

- **2PC Coordinator**: Manages distributed transactions across multiple participants
- **Sample Participant**: In-memory ledger with prepare/commit/abort semantics
- **SQLite Persistence**: Transaction log with WAL mode for crash recovery
- **HTMX Frontend**: Real-time dashboard with 5-second auto-refresh
- **Async API**: `202 Accepted` with polling, optional `?sync=true` for blocking
- **Idempotency**: Caller-supplied keys, automatic deduplication
- **Normalized Schema**: Separate `transactions` and `votes` tables
- **Automatic Recovery**: On restart, coordinator re-drives non-terminal transactions to completion
- **Prometheus Metrics**: Built-in metrics endpoint for monitoring

## Rationale

### What Problem Does This Solve?

In distributed systems and microservices architectures, operations often span multiple services and databases. For example, an e-commerce order might need to:

1. Reserve inventory (Inventory Service)
2. Authorize payment (Payment Service)
3. Create an order record (Order Service)

If any step fails, the entire operation must be rolled back to maintain consistency. However, each service typically has its own private database, so a traditional ACID transaction across databases isn't possible.

### The 2-Phase Commit Protocol

The Two-Phase Commit (2PC) protocol provides **atomicity across heterogeneous resources** by coordinating participants through two phases:

- **Phase 1 (Prepare)**: Coordinator asks all participants if they can commit. Each participant:
  - Acquires necessary locks/leases
  - Writes to its own write-ahead log (WAL)
  - Votes "yes" (I can commit) or "no" (I cannot)
- **Phase 2 (Commit/Abort)**: 
  - If all participants vote "yes", coordinator sends commit command → all finalize
  - If any participant votes "no" or times out, coordinator sends abort → all roll back

This ensures **all-or-nothing** semantics across services, even when they use different databases or data stores.

### When to Use 2PC

2PC is appropriate when:

- **Strong consistency is required**: You cannot tolerate partial success (e.g., money deducted but inventory not reserved)
- **Few participants**: Typically 2-10 services. More participants increase latency and failure probability.
- **Short-lived operations**: The blocking nature of 2PC is acceptable for operations that complete within seconds.
- **Participants support prepare/commit/abort**: Each service can implement idempotent prepare and commit operations.

### When NOT to Use 2PC

Consider alternatives when:

- **High availability is critical**: 2PC blocks during coordinator or participant failures. Use **Saga pattern** with compensating actions for eventual consistency.
- **Long-running operations**: 2PC holds locks/resources during the protocol. Break the operation into idempotent steps with **outbox pattern**.
- **Many participants**: Latency grows with each additional participant. Use **eventual consistency** and async messaging.
- **Heterogeneous systems with no prepare support**: Some databases/stores don't expose a prepare phase. Use application-level compensation instead.

### Trade-offs

| Aspect | 2PC | Saga | Outbox |
|--------|-----|------|--------|
| Consistency | Strong (ACID-like) | Eventual | Eventual |
| Latency | Medium (2 round trips) | Low (fire-and-forget) | Low |
| Blocking | Yes (during prepare/commit) | No | No |
| Complexity | Medium (coordinator + participants) | High (compensations) | Medium |
| Failure handling | Coordinator crash → recovery | Compensating transactions | Idempotent consumers |

### Use Cases

This implementation is well-suited for:

- **Financial transactions**: Transfer money between accounts, ensure both debit and credit succeed or both fail
- **Order processing**: Reserve inventory, authorize payment, create order atomically
- **Booking systems**: Reserve seat, process payment, issue ticket
- **Resource orchestration**: Allocate multiple resources (compute, storage, network) as a single unit

### Why This Implementation?

This Go-based 2PC service provides:

- **Language-agnostic HTTP API**: Any language can be a participant (just implement the 4 endpoints)
- **Production-ready features**: Crash recovery, metrics, monitoring, HTMX dashboard
- **Reference implementation**: Learn 2PC by reading the code and running the sample
- **Extensible**: Add your own participants with business logic specific to your domain

## Project Structure

```
2PhaseCommitAPI/
├── cmd/
│   ├── coordinator/     # Coordinator service
│   └── participant/     # Sample participant service
├── internal/
│   ├── api/             # HTTP handlers and types
│   ├── config/          # Configuration loading
│   ├── coord/           # 2PC state machine and coordinator logic
│   ├── metrics/         # Prometheus metrics instrumentation
│   ├── pax/             # Participant client and server
│   └── txnlog/          # SQLite transaction log
├── web/
│   └── templates/       # HTMX + Pico.css templates
├── Makefile
├── test-api.sh          # Integration test script
├── test-recovery.sh     # Crash recovery test script
├── test-metrics.sh      # Metrics verification script
└── README.md
```

## Quick Start

### Prerequisites

- Go 1.26+
- No external database required (SQLite is embedded via modernc.org/sqlite)

### Build

```bash
make build
```

Produces two binaries:
- `coordinator` — the 2PC coordinator service
- `participant` — sample participant service (in-memory ledger)

### Run Locally

**Option A: Using separate terminals (recommended for development)**

Terminal 1 — Start the first participant (accounts-svc):
```bash
PORT=:8081 ./participant
```

Terminal 2 — Start the second participant (inventory-svc):
```bash
PORT=:8082 ./participant
```

Terminal 3 — Start the coordinator:
```bash
PORT=:8080 ./coordinator
```

The coordinator automatically registers the two participants at `localhost:8081` and `localhost:8082`.

**Option B: Using background processes**

```bash
# Clean any previous database
rm -f txn.db

# Start participants in background
PORT=:8081 ./participant & PARTICIPANT1_PID=$!
PORT=:8082 ./participant & PARTICIPANT2_PID=$!

# Wait for them to start
sleep 2

# Start coordinator
PORT=:8080 ./coordinator & COORDINATOR_PID=$!

# When done, stop all
kill $COORDINATOR_PID $PARTICIPANT1_PID $PARTICIPANT2_PID
```

### Access the Frontend

- **Dashboard**: http://localhost:8080/
- **New Transaction Form**: http://localhost:8080/new
- **Transaction Detail**: http://localhost:8080/tx/{id}

The dashboard auto-refreshes every 5 seconds and shows all transactions with their current status and participant votes.

### Try It Out

1. Open http://localhost:8080/new
2. Fill in:
   - **Topic Key**: `order-123`
   - **Operations**:
     - Participant: `accounts-svc`, Action: `authorize`, Params: `{"account":"A","amount":10000}`
     - Participant: `inventory-svc`, Action: `reserve`, Params: `{"item":"widget","quantity":1}`
   - **Timeout (ms)**: `10000`
3. Click "Create Transaction"
4. Watch the dashboard update automatically
5. Click the transaction ID to see detailed vote status

Or use the API directly:

```bash
curl -X POST http://localhost:8080/transactions \
  -H "Content-Type: application/json" \
  -d '{
    "topic_key": "order-123",
    "operations": [
      {
        "participant": "accounts-svc",
        "action": "authorize",
        "params": {"account": "A", "amount": 10000}
      },
      {
        "participant": "inventory-svc",
        "action": "reserve",
        "params": {"item": "widget", "quantity": 1}
      }
    ],
    "timeout_ms": 10000
  }'
```

Poll for status:

```bash
curl http://localhost:8080/transactions/tx_order-123-*
```

### API Usage

#### Create Transaction (async)

```bash
curl -X POST http://localhost:8080/transactions \
  -H "Content-Type: application/json" \
  -d '{
    "topic_key": "order-123",
    "operations": [
      {
        "participant": "accounts-svc",
        "action": "authorize",
        "params": {"account": "A", "amount": 10000}
      },
      {
        "participant": "inventory-svc",
        "action": "reserve",
        "params": {"item": "widget", "quantity": 1}
      }
    ],
    "timeout_ms": 10000
  }'
```

Response:
```json
{
  "id": "tx_order-123-1a2b3c4d",
  "status": "INIT",
  "topic_key": "order-123",
  "message": "Transaction accepted, poll /transactions/{id} for status"
}
```

#### Get Transaction Status

```bash
curl http://localhost:8080/transactions/tx_order-123-1a2b3c4d
```

#### List Transactions

```bash
curl http://localhost:8080/transactions
```

#### Cancel Transaction

```bash
curl -X POST http://localhost:8080/transactions/tx_order-123-1a2b3c4d/cancel \
  -H "Content-Type: application/json" \
  -d '{"reason": "operator cancelled"}'
```

### Frontend

The built-in HTMX dashboard provides:
- Real-time transaction list (auto-refresh every 5 seconds)
- Transaction detail view with participant status
- Form to create new transactions
- Cancel button for in-flight transactions

Access at: http://localhost:8080

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `:8080` (coordinator), `:8081` (participant) | HTTP listen address |
| `TXNDB_PATH` | `./txn.db` | SQLite database path |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown timeout |

### Coordinator Participant Registration

In `cmd/coordinator/main.go`, the `participantURLs` map defines which participants are available:

```go
participantURLs := map[string]string{
    "accounts-svc":  "http://localhost:8081",
    "inventory-svc": "http://localhost:8082",
}
```

Add or modify entries to match your participant services.

## Architecture

### Coordinator State Machine

```
INIT ──prepare──▶ PREPARING ──all YES──▶ COMMITTING ──all commit──▶ COMMITTED
                       │                                      ▲
                       │                                  commit
                       ▼                                      │
                   any NO ──▶ ABORTING ──all abort──▶ ABORTED
```

### Transaction Log Schema

```sql
CREATE TABLE transactions (
    id TEXT PRIMARY KEY,
    topic_key TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    decision_at TIMESTAMP,
    decided_by TEXT,
    error_message TEXT
);

CREATE UNIQUE INDEX idx_transactions_topic_inflight 
ON transactions(topic_key) 
WHERE status NOT IN ('COMMITTED','ABORTED','FAILED');

CREATE TABLE votes (
    tx_id TEXT NOT NULL,
    participant TEXT NOT NULL,
    vote TEXT NOT NULL,
    reason TEXT,
    prepared_at TIMESTAMP,
    committed_at TIMESTAMP,
    aborted_at TIMESTAMP,
    PRIMARY KEY (tx_id, participant),
    FOREIGN KEY (tx_id) REFERENCES transactions(id) ON DELETE CASCADE
);
```

### Participant Protocol

All participants must implement:

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/prepare` | POST | Idempotent vote yes/no with action/params |
| `/commit` | POST | Idempotent finalize |
| `/abort` | POST | Idempotent rollback |
| `/tx/{id}` | GET | Query participant's state for recovery |

## Testing

Run the API test script:

```bash
chmod +x test-api.sh
./test-api.sh
```

Run the recovery test script:

```bash
chmod +x test-recovery.sh
./test-recovery.sh
```

Run the metrics test script:

```bash
chmod +x test-metrics.sh
./test-metrics.sh
```

## Development

```bash
# Build
make build

# Run coordinator
make run-coordinator

# Run participant
make run-participant

# Clean
make clean

# Format
make fmt

# Vet
make vet
```

## Notes

- This is a **reference implementation** for educational purposes
- Single coordinator (no HA/Raft)
- In-memory participant state (no persistence)
- No authentication/authorization
- Failure injection stubbed in tests only

## License

MIT
