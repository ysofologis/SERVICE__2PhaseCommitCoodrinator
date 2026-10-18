# Two-Phase Commit API — Backend Architecture

## Overview

A Go web service implementing the **Two-Phase Commit (2PC)** distributed transaction protocol. The system has two roles:

- **Coordinator** — orchestrates transactions across multiple participants
- **Participant** — a service that participates in transactions, voting yes/no during prepare phase

Built as a single static binary (no CGO) using `modernc.org/sqlite` for the transaction log.

## Project Structure

```
cmd/
  coordinator/main.go     # Coordinator entry point
  participant/main.go     # Participant entry point
internal/
  api/                    # HTTP handler layer
    handlers.go           # All HTTP endpoints
    types.go              # Request/response structs
  config/config.go        # Env-based configuration
  coord/                  # Core 2PC logic
    coordinator.go        # Coordinator state machine + protocol
    state.go              # State definitions & validation
    *_test.go             # Unit tests
  pax/                    # Participant protocol layer
    client.go             # HTTP client for participant calls
    server.go             # HTTP server implementing participant protocol
  txnlog/                 # Transaction persistence
    log.go                # Log interface
    sqlite.go             # SQLite implementation
  metrics/metrics.go      # Prometheus metrics
web/templates/            # HTMX-powered frontend
clients/                  # End-user-facing desktop client (proof-of-coordinator)
    PLAN.md               # Design doc: AvaloniaUI 11 .NET 8 trading client
    avalonia/             # (future) dotnet project — see clients/PLAN.md
```

---

## The 2PC Protocol Flow

### Phase 1: Prepare

1. Client posts `POST /transactions` with a `topic_key`, operations, and timeout
2. Coordinator creates an `INIT` transaction in its SQLite log
3. Returns `202 Accepted` immediately (async mode) with the tx ID
4. Coordinator fans out `POST /prepare` to all participants **concurrently**
   - Each participant receives: `{action, params}`
   - Participants vote: `yes` or `no`
5. If any participant votes `no` or errors → abort path
6. If all vote `yes` → proceed to commit phase

### Phase 2: Commit

1. Coordinator transitions to `COMMITTING`
2. Sends `POST /commit` to all participants concurrently
3. On success → state becomes `COMMITTED`
4. If any commit fails → state becomes `FAILED` (coordinator gives up)

### Abort Path

Triggered by:
- A participant voting `no` during prepare
- A participant error/timeout during prepare
- Operator calling `POST /{txid}/cancel`
- Coordinator timeout (context deadline exceeded)

Coordinator sends `POST /abort` to all participants that prepared.

---

## State Machine

```
    ┌─────┐
    │ INIT│
    └──┬──┘
       │ StartTransaction
       ▼
  ┌──────────┐
  │ PREPARING │◄── AddParticipant (while here only)
  └──┬───────┘
     │ All votes collected
     ├──── Any no/error? ──► ABORTING ──► ABORTED
     │ All yes?
     ▼
  ┌──────────┐
  │ COMMITTING │
  └──┬───────┘
     ├──── All commits OK? ──► COMMITTED
     ├──── Some failed? ──────► FAILED
```

Terminal states: `COMMITTED`, `ABORTED`, `FAILED`. Once terminal, no further mutations.

---

## Data Model

### Transaction Table (SQLite)

| Column | Type | Notes |
|--------|------|-------|
| `id` | TEXT | `tx_{topic_key}-{8hex}` |
| `topic_key` | TEXT | Business identifier for deduplication |
| `status` | TEXT | State machine state |
| `created_at` | DATETIME | UTC |
| `updated_at` | DATETIME | Last state change |
| `decision_at` | DATETIME | Null until terminal |
| `decided_by` | TEXT | `"protocol"` or recovery reason |
| `error` | TEXT | Failure reason if applicable |

### Votes Table (SQLite)

| Column | Type | Notes |
|--------|------|-------|
| `tx_id` | TEXT | FK to transaction |
| `participant` | TEXT | Service name |
| `vote` | TEXT | `yes`/`no`/`committed`/`aborted`/`timeout` |
| `reason` | TEXT | Why no/vote failed |
| `at` | DATETIME | Vote timestamp |

**Partial unique index** on `(tx_id, participant)` — ensures a participant can only vote once per transaction.

---

## Coordinator Internals

### txHandle

An in-memory handle wrapping each active transaction:

```go
type txHandle struct {
    tx           *txnlog.Transaction  // Persistent record
    state        State                // Current FSM state
    votes        map[string]voteInfo  // Per-participant votes
    cancel       chan struct{}        // Signal to abort
    done         chan struct{}        // Closed when complete
    participants []ParticipantOp      // Snapshot of ops
    mu           sync.RWMutex
}
```

Key properties:
- **Non-blocking**: `StartTransaction` returns after writing to DB and launching `go c.runTransaction(...)`
- **Idempotent**: In-flight transactions share the same tx ID via topic key lookup
- **Safe teardown**: On completion, `done` is closed, handle removed from `inFlight`, metrics recorded

### runTransaction

The main async goroutine:

1. Transitions to `PREPARING`
2. Creates a timeout context (`timeoutMs`)
3. Fan-out `Prepare` calls via WaitGroup (parallel, concurrent HTTP requests)
4. Collects results under mutex protection
5. If any error/no-vote → triggers `abortTransaction`
6. If all yes → transitions to `COMMITTING` then calls `commitTransaction`

### AddParticipant

Allows dynamic addition of participants while in `PREPARING`:
- Appends to `handle.participants`
- Immediately sends `POST /prepare` to the new participant
- If the new participant votes no → triggers abort
- If this completes all voters' yes votes → transitions to `COMMITTING`

Thread-safety: `runTransaction` snapshots `participants` before iterating; subsequent adds are picked up because they modify `handle.participants` directly. However, there's no explicit lock around the snapshot-vs-mutate boundary — race window exists if called extremely fast.

### Recovery

Runs at startup after the HTTP server begins listening:

1. Query SQLite for non-terminal transactions
2. For each, query participant `/tx/{id}` status endpoints
3. Decision matrix:
   - **anyAborted**: send abort to all remaining → `ABORTED`
   - **anyCommitted** but not all: mark `FAILED` (inconsistent)
   - **allPrepared**, none aborted: attempt `commitTransaction` → `COMMITTED` or `FAILED`
   - **Mixed/uncertain**: mark `FAILED`

Known limitation: `buildParticipantOps()` returns ops without the original `Action` and `Params` fields. Recovery uses `commitTransaction` and `abortTransaction` which don't need these fields — safe. But recovery cannot re-run `prepare` since it would be a duplicate call.

---

## Participant Protocol

Participants implement an HTTP server at port 8081 (default). Three endpoints:

```
POST /prepare    → { "vote": "yes"|"no", "reason": "" }
POST /commit     → 200 OK
POST /abort      → 200 OK
GET  /tx/:id     → { "status": "prepared"|"committed"|"aborted" }
```

### Ledger Implementation

The reference participant maintains an in-memory ledger map (`map[string]int`). Operations:

| Action | Logic |
|--------|-------|
| `create` | Record debit: `ledger[account] -= amount` |
| `authorize` | Check balance >= amount. Insufficient → vote no. Otherwise: reserve delta. |
| `reserve` | Deduct reserved delta: `ledger[account] -= delta`. No funds → vote no. |
| `log` | Always vote yes silently (read-only operation) |
| Unknown action | Always vote yes silently (safe default) |

`/tx/{id}` queries track per-id preparation status. After prepare, status transitions through `prepared` → `committed` or `aborted`.

---

## HTTP Routing (Coordinator)

```
GET  /healthz               ← Health check (always 200)
GET  /readyz                ← Readiness (DB connectivity test)
POST /transactions           ← Create transaction
GET  /transactions           ← List recent (paginated)
GET  /transactions/:id       ← Get transaction detail
POST /transactions/:id/participants  ← Add participant mid-flight
POST /transactions/:id/cancel    ← Cancel transaction
GET  /metrics               ← Prometheus exposition
GET  /                      ← Dashboard HTML
GET  /new                   ← New transaction form HTML
GET  /tx/:id                ← Transaction detail HTML
```

### Response Codes

- **200 OK**: Sync mode completed successfully, or GET/POST for status/detail
- **202 Accepted**: Async mode — transaction started, poll for status
- **400 Bad Request**: Missing required fields, invalid JSON/form
- **404 Not Found**: Unknown tx ID or participant name
- **408 Request Timeout**: Sync mode timed out waiting for terminal
- **409 Conflict**: Topic key conflict (already committed/aborted), or invalid state transition
- **422 Unprocessable Entity**: Transaction aborted (participant voted no)
- **500 Internal Server Error**: Unexpected failure

---

## Persistence Design

### WAL Mode

SQLite runs in WAL (Write-Ahead Logging) mode for concurrent read/write safety. Database file persists across process restarts, enabling the recovery mechanism.

### Transaction Lifecycle in Storage

1. `INSERT INTO transactions` with `INIT` status → immediate durability
2. `INSERT INTO votes` for each `yes` vote → durable evidence
3. Status updates (`UPDATE transactions`) on each state transition
4. Final state update includes `decision_at` and `decided_by`

### Idempotency

The partial unique index on `(tx_id, participant)` in the votes table prevents duplicate vote records. In-flight deduplication happens at the coordinator level via `FindInFlightByTopic`.

---

## Metrics (Prometheus)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `transactions_started_total` | Counter | — | Total tx starts |
| `transactions_completed_total` | Counter | `status` | Completed tx by final state |
| `transaction_duration_seconds` | Histogram | `status` | End-to-end duration |
| `votes_total` | Counter | `participant`, `vote` | Votes cast |
| `recovery_attempts_total` | Counter | — | Recovery runs |
| `recoveries_completed_total` | Counter | — | Successful recoveries |
| `errors_total` | Counter | `type` | Error categories |

Error types: `prepare_errors`, `prepare_panic`, `commit_failed`, `commit_panic`, `abort_failed`, `abort_panic`, `record_vote_failed`, etc.

---

## Frontend (HTMX)

The dashboard uses **HTMX** for progressive enhancement — traditional server-side rendered HTML with AJAX-like interactivity:

### Dashboard (`dashboard.html`)
- Lists recent transactions in a table
- Hx-get polls refresh every 2s for in-flight txns
- Color-coded status badges

### Transaction Detail (`tx_detail.html`)
- Shows full transaction data with participant votes
- Auto-refreshes while in PREPARING/COMMITTING/ABORTING
- Disables controls once terminal

### New Transaction Form (`new_tx.html`)
- Dynamic participant rows (add/remove)
- POSTs to `/transactions` (async mode by default)
- Redirects to detail page after receiving tx ID

---

## Thread Safety Summary

| Component | Protection |
|-----------|------------|
| `Coordinator.mu` | RWMutex protecting `inFlight`, `participants`, `startTimes` maps |
| `txHandle.mu` | RWMutex protecting `votes` map during parallel prepare/commit/abort |
| SQLite connections | One `*sql.DB` instance; `modernc.org/sqlite` is thread-safe |
| Template execution | Pre-parsed templates; `template.ExecuteTemplate` is thread-safe for reading |

Known gaps:
- `AddParticipant` modifies `handle.participants` slice while `runTransaction` may be reading it concurrently. Currently mitigated by making a copy in `runTransaction`, but there's a small TOCTOU window between snapshot and iteration start.
- Recovery creates bare `txHandle`s with empty `votes` maps and zero-state `state` — relies on deterministic recovery decision logic rather than in-memory state consistency.

---

## Testing Strategy

### Unit Tests (`*_test.go`)
- **State machine**: Transition validation, terminal checks
- **ID generation**: Deterministic salt uniqueness
- **SQLite CRUD**: In-memory DB, transaction lifecycle
- **Participant HTTP**: Round-trip prepare/commit/abort/query

### Integration Tests (`integration_test.go`)
- Uses `httptest.Server` with real HTTP participant clients
- Full flow: happy-path commit, vote-no abort, timeout, cancel, dedup, add-participant
- Mock participants configurable per-test (vote yes/no/delay/fail)

---

## Planned Clients: AvaloniaUI 2PC Trading Client

**See `clients/PLAN.md`** for the full specification.

### Scenario

A cross-platform .NET 8 / AvaloniaUI 11 desktop client lets a single user (or two users in two windows) place buy/sell orders against the existing 2PC coordinator. The app renders the 2-phase commit lifecycle in real time and includes a "Race Demo" modal that drives two simultaneous transactions against a scarce security to prove the coordinator prevents the over-selling race condition.

### Architecture

```
            ┌─────────────────────────────────────────────┐
            │  AvaloniaUI 11 desktop client (.NET 8)        │
            │   Views ⇄ ViewModels ⇄ CoordinatorClient      │
            └─────────────────┬────────────────────────────┘
                              │ HttpClient / JSON
                              ▼
                ┌─────────────────────────────┐
                │  2PC Coordinator (Go)       │
                └────────────┬────────────────┘
                             │ HTTP/JSON
              ┌──────────────┴──────────────┐
              ▼                             ▼
     accounts-svc (Go)            inventory-svc (Go)
```

Unlike the earlier cross-language participants plan, this client does **not** implement new protocol participants. The Go sample participant (`cmd/participant`) is the only participant in the system. The C# code is a thin client wrapper plus a visual proof of correctness.

### Key Mechanism

- Each participant owns its own SQLite file (no shared DB at runtime)
- During `/prepare`, participants write `write_locks` rows (tx-token → resource)
- On `/commit`: apply in-memory deltas; on `/abort`: discard and release locks
- External writes must check `write_locks` before modifying locked resources
- This provides single-writer guarantee per resource across parallel HTTP endpoints

<!-- project: /storage/drive-S/Work/SideProjects/2PhaseCommitAPI -->
