# TradingClient — End-User HOWTO

> **Audience:** Anyone who wants to drive the 2-Phase Commit coordinator
> and watch the protocol work — a developer demoing the system, a
> reviewer clicking through it, or a stakeholder who wants to see
> atomicity proven without reading the Go source.
>
> This is **not** the developer-onboarding doc. For project layout,
> build commands, and the test suite, see [`README.md`](README.md).
> For the design rationale, see [`../PLAN.md`](../PLAN.md).

---

## 1. What the app does (60-second tour)

You sit in front of a single window with two panes. On the **left** is
an "Orders" list — every transaction the coordinator has run, newest
first, with a colored status badge (`PREPARING` / `COMMITTED` /
`ABORTED` etc.). On the **right** is the **Order detail** for the row
you've selected — it shows the full 2PC lifecycle: timestamps for each
phase, the per-participant vote and outcome, and any error message.

You can:

- **Place an order** — open a form, pick a ticker and quantity, hit
  Submit. The app talks to the coordinator, which runs 2PC against the
  two Go participant services (one for cash, one for inventory).
- **Watch it commit or abort in real time** — the detail pane updates
  once per second until the transaction reaches a terminal state.
- **Run the Race Demo** — the killer feature. Two simultaneous buy
  orders on the same scarce security. Exactly one should commit; the
  other should abort. The app tells you which it is and whether the
  coordinator behaved correctly.
- **Cancel an in-flight transaction** — useful for seeing the cancel
  path (PREPARING → ABORTING → ABORTED).
- **Reset the demo data** — the participant's in-memory cash and
  inventory are re-seeded, so you can re-run the race demo.

The app is a thin visual client. It does **no** business logic of its
own — every status you see came from the Go coordinator.

---

## 2. Before you launch (one-time setup)

The app needs three things running on the same machine:

| Service | Port | What it is | How to start it |
|---|---|---|---|
| **Coordinator** | 8080 | The Go HTTP service that runs the 2PC protocol | `make run` in the 2PC repo (or run `coordinator` manually) |
| **accounts-svc** | 8081 | Go sample participant, holds cash balances in memory | same `make run` target brings this up |
| **inventory-svc** | 8082 | Go sample participant, holds share inventory in memory | same `make run` target brings this up |

### Check the Go side is up

In a terminal:

```bash
curl -s http://localhost:8080/healthz   # → {"status":"ok"}
curl -s http://localhost:8080/readyz    # → {"status":"ready"}
```

If either returns non-200, the coordinator isn't healthy. The Avalonia
app's status bar will show a red "coordinator unreachable" banner.

### Install .NET 8 SDK (one-time, per machine)

The Avalonia app needs the .NET 8 SDK. The project pins the exact
version in `global.json`:

```bash
cd clients/avalonia
dotnet --version    # should print 8.0.4xx (matches global.json)
```

If you don't have .NET installed, grab it from
<https://dot.net/download> or your platform's package manager.
On Manjaro: `sudo pacman -S dotnet-sdk-8.0`.

---

## 3. Launch the app

```bash
cd clients/avalonia
dotnet run --project src/TradingClient
```

First launch takes ~15 seconds (NuGet restore + Avalonia build).
Subsequent launches are ~3 seconds.

A window opens with a dark theme, two panes, and a status bar at the
top. The status bar should turn green within a second, reading
"✓ connected to coordinator". If it stays red, see §8 (Troubleshooting).

### Targeting a non-localhost coordinator

The app reads the URL from `appsettings.json` (default
`http://localhost:8080`) or the `Coordinator__Url` environment
variable:

```bash
Coordinator__Url=http://192.168.1.42:8080 dotnet run --project src/TradingClient
```

This is also where you'd point it at a coordinator on a teammate's
machine. The app doesn't care; the URL is just a string.

---

## 4. Place an order (the basic flow)

1. Click **Place Order** in the bottom action bar.
2. Fill in the form:
   - **Ticker** — `AAPL`, `MSFT`, or `GOOG` (any string is accepted;
     these three are just the dropdown presets)
   - **Side** — `BUY` or `SELL`
   - **Quantity** — how many shares
   - **Price** — the unit price; the app computes the notional
     (`qty × price`) and sends it as the `amount` to `accounts-svc`
   - **Account** — which cash account to debit (the participant
     seeds `A=10000` and `B=10000` at startup; any other name votes
     no with reason "insufficient funds")
   - **Timeout (ms)** — 10 000 by default; the coordinator's per-tx
     budget
   - **Synchronous** — leave unchecked for the normal async flow
     (returns `202 Accepted` immediately, you watch the lifecycle
     via the detail pane). Check it for `?sync=true` mode (the
     request blocks until terminal; useful when you want a single
     "done" event instead of polling).
3. Click **Submit**.
4. The detail pane opens automatically on the new transaction. Watch
   the status badge transition through `INIT → PREPARING → COMMITTING
   → COMMITTED` (or, if something failed, into `ABORTING → ABORTED`).
5. Look at the **Participants** table in the detail pane:
   - `accounts-svc` voted `yes → committed` (or `yes → aborted`)
   - `inventory-svc` voted `yes → committed` (or similar)
   - The **reason** column shows why a vote was no (e.g.,
     "insufficient funds", "out of stock").

### Try the failures (no extra setup)

- **Insufficient funds** — set Account to `C` (or any other name
  not in `{A, B}`) and Price × Quantity > 0. `accounts-svc` votes no.
  The coordinator receives one yes and one no, aborts both, the
  detail pane shows `ABORTED` with reason "insufficient funds".
- **Sync mode** — tick the box, submit an order that you know will
  fail. The form stays disabled until the server returns the
  terminal state. The result line shows the outcome directly.

---

## 5. Run the Race Demo (the killer feature)

This is what the app exists to demonstrate. Two end-users click Buy on
the same scarce security at the same time; the coordinator must
serialize them and let only one through.

### What "scarce" means here

The Go sample participant ships with seeded state:

- Cash: `A=10000`, `B=10000`
- Inventory: `AAPL=100`, `MSFT=500`, `GOOG=500`

A buy of 100 AAPL at $15 = $1500 notional. Account A has $1500
exactly. So **the first buy drains the account**, the second buy
either has no inventory (vote no on `inventory-svc`) or no cash (vote
no on `accounts-svc`).

### The steps

1. **Make sure the participant is fresh.** Click **Dev tools** in the
   bottom action bar (or open the Dev drawer), then **Reset demo
   data**. This calls `POST /admin/reset` on both participants,
   re-seeding them to the initial state. Wait for the green
   "✓ demo data reset" message.
2. Click **Race demo** in the bottom action bar.
3. Confirm the form values: Ticker `AAPL`, Quantity `100`, Price
   `15.00`, Account `A`. (These are the defaults.)
4. Click **Start both**. The app fires two `POST /transactions?sync=true`
   requests ~50ms apart and waits for both to reach a terminal state.
5. Within a second or two, the modal renders the outcome:
   - One side: `tx_... → COMMITTED` (green)
   - Other side: `tx_... → ABORTED (reason)` (red)
   - Bottom verdict: `✓ Race prevented: exactly one commit, one abort. Coordinator wins.`

### If both come back COMMITTED

The app will say `✗ OVER-SELL: both transactions committed. Coordinator has a bug.`

**Don't shrug this off.** That verdict means the 2PC protocol is no
longer preventing the race condition. Stop and check:

- Did the Go coordinator restart recently? (The log file is at the
  path set by `TXNDB_PATH`, default `txn.db`.)
- Is there a coordinator log line saying "recovery" for either of
  the two transactions? If so, the recovery path may have a bug.
- Re-run `go test -count=1 ./...` from the 2PC repo. If `TestIntegrationAddParticipant`
  is red, that's a regression that needs a fix.

If the demo is clean, you're seeing the 2PC protocol do its job
in real time. Worth showing to anyone who asks "is this thing
actually atomic?"

### Re-run

After the first run, the participant's account A balance is $0 and
AAPL inventory is 0. Click **Reset demo data** in the Dev drawer to
re-seed, then run the race again. Each fresh run should produce one
COMMIT, one ABORT.

---

## 6. Cancel an in-flight transaction

Useful for seeing the cancellation path (the third state machine
terminal, alongside COMMITTED and ABORTED).

1. Place an order with a long timeout (say, 30 000 ms) and a low
   Price × Quantity. The transaction will sit in `PREPARING` for
   long enough to cancel.
2. While the detail pane shows `PREPARING`, click **Cancel selected**
   in the bottom action bar.
3. The badge transitions `PREPARING → ABORTING → ABORTED`. The
   reason in the detail pane is `operator-cancelled`.

The cancel is also exposed at `POST /transactions/{id}/cancel` for
CLI testing:

```bash
curl -X POST http://localhost:8080/transactions/tx_xxx/cancel
```

---

## 7. Dev tools drawer (settings, health, reset)

Click **Dev tools** in the bottom action bar to expand the drawer.
Three things live there:

- **Coordinator URL** — defaults to `http://localhost:8080`. Edit and
  click **Health check** to re-probe.
- **Health check** — calls `GET /healthz` and `GET /readyz`. Shows a
  green/red banner.
- **Reset demo data** — calls `POST /admin/reset` on both
  participants. Re-seeds the in-memory state. The `POST /admin/reset`
  endpoint is dev-only; in production it would be removed or
  guarded by auth.

The drawer is intentionally not a settings dialog with persistence
yet — that's a v2 feature.

---

## 8. Troubleshooting

### "Coordinator unreachable" red banner on startup

1. Is the coordinator running? `curl http://localhost:8080/healthz`
   should return 200. If it doesn't, start the Go side:
   `cd /path/to/2PhaseCommitAPI && make run`.
2. Is the URL right? The default is `http://localhost:8080`. If your
   coordinator is on another host, set
   `Coordinator__Url=http://host:port` before launching.
3. Is a firewall blocking loopback? Unlikely on the same machine,
   but if the coordinator is on another host, check that the port
   is reachable from the app's host.

### App launches but Place Order says "unknown participant: ..."

The coordinator doesn't know the participant name you used. The
default Go coordinator pre-registers `accounts-svc` (port 8081) and
`inventory-svc` (port 8082). If you've changed the names, update both
the participant's startup and the place-order form. The two
participant names are in:

- Go: `cmd/coordinator/main.go` (the `participants` map)
- C#: `PlaceOrderViewModel.BuildRequest` (the `Operation.Participant`
  string)

### The race demo says "OVER-SELL" — the coordinator has a bug!

Don't keep re-running it. The verdict is meaningful. Do this:

1. Save the coordinator log to a file: `journalctl -u 2pc > crash.log`
   (or wherever your logs go).
2. From the 2PC repo, run `go test -count=1 ./...` to confirm the
   regression is reproducible.
3. Look at the two `tx_...` IDs in the OVER-SELL message and grep
   the coordinator log for them. You should see the prepare votes,
   the commit requests, and the commit acks.
4. The bug is in either the prepare fan-out (allowing two votes for
   a single scarce resource), the commit fan-out (committing both
   without checking), or the recovery path (re-driving a partial
   commit).

### "dotnet --version" complains about the SDK

The project pins the SDK to 8.0.x via `global.json`. If your
installed SDK is 9.x, you need 8.x too. `dotnet --list-sdks` shows
what you have; install 8.0.4xx alongside.

### App crashes immediately on Linux

Check that your desktop session is running. AvaloniaUI needs an
X server or Wayland. On a headless machine (e.g., CI), the app
won't open a window — but the test suite (`dotnet test`) will still
run.

---

## 9. Reading the order detail pane (annotated)

```
┌──────────────────────────────────────────────────────────────┐
│ buy-AAPL-100-2026-10-18T17-42-01Z-a1b2c3                       │
│ [● COMMITTED]   tx_buy-AAPL-100-...-a1b2c3                     │
│                                                              │
│ TIMELINE                                                     │
│  created          17:42:01                                    │
│  accounts-svc     17:42:01                                    │
│    prepared                                                 │
│  inventory-svc    17:42:01                                    │
│    prepared                                                 │
│  committed        17:42:02   ← ~1.1s from start              │
│  accounts-svc     17:42:02                                    │
│    committed                                                │
│  inventory-svc    17:42:02                                    │
│    committed                                                │
│                                                              │
│ PARTICIPANTS                                                 │
│  accounts-svc     vote: yes     → committed                  │
│  inventory-svc    vote: yes     → committed                  │
│                                                              │
│ PHASES                                                       │
│  prepare: ok                                                 │
│  commit:  ok                                                 │
└──────────────────────────────────────────────────────────────┘
```

What to look for:

- **The status badge is the only field that changes in real time.**
  Everything else in the detail pane is the snapshot from the last
  poll (1s cadence).
- **The TIMELINE is sorted chronologically** — created first,
  prepared/committed/aborted next, in the order the coordinator
  recorded them. The gap between "created" and "committed" is your
  end-to-end 2PC latency.
- **The PARTICIPANTS table shows per-participant vote + outcome.**
  If you see `vote: no` and the final is `aborted`, the `reason`
  column tells you why.
- **PHASES is a coarser summary** — `prepare: ok` means all prepares
  succeeded (or none were attempted because the tx was rejected up
  front). `commit: ok` means all commits returned 200.

---

## 10. What this app is **not**

- **Not a participant service.** The Go sample participant
  (`cmd/participant`) is the only 2PC participant in the system.
  The C# code is a client, not a participant.
- **Not a production trading app.** No order routing, no FIX, no
  auth, no persistence beyond the in-memory participant state.
- **Not a multi-coordinator setup.** The app talks to one
  coordinator. Multi-coordinator / HA is a coordinator-side concern
  out of scope.
- **Not a load test.** The Go integration tests in
  `internal/coord/integration_test.go` cover the protocol logic;
  this app is a visual proof, not a new test surface.

---

## 11. Where to look next

- **What is 2PC and why does it matter here?** — see the top-level
  [`../../README.md`](../../README.md) and
  [`../../BACKEND.md`](../../BACKEND.md).
- **The protocol spec** — see
  [`../../user-tasks/2pc-service/USER-TASK.md`](../../user-tasks/2pc-service/USER-TASK.md)
  for the full functional spec, the v2 acceptance criteria, and
  the state-machine diagram.
- **The end-user scenario (race demo design)** — see
  [`../PLAN.md`](../PLAN.md), section 6.
- **The Go code** — `internal/coord/coordinator.go` is the main
  file. `internal/pax/server.go` is the participant.
- **The C# code** — see [`README.md`](README.md) for the project
  layout and the role of each ViewModel.

If you find a bug — in the UI, the protocol, or the demo state — file
an issue with the `transaction_id` and a screenshot. The Go tests are
deterministic; if the demo is wrong, the test suite is wrong too.
