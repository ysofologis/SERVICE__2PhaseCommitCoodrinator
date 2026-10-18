---
title: AvaloniaUI 2PC Trading Client — End-User Demonstration App
created: 2026-10-18
tags: [type/design-doc, type/user-task, .net, avalonia, client]
status: PROPOSED
target-stack: [.NET 8, AvaloniaUI 11, CommunityToolkit.Mvvm, HttpClient, System.Text.Json]
supersedes: 2026-09-17 cross-language participants plan (accounts-svc + orderbook-svc)
---

# AvaloniaUI Trading Client — 2PC End-User Proof

> **What this replaces:** The previous `clients/PLAN.md` (2026-09-17) called for
> building two cross-language 2PC *participants* (C# accounts-svc + Python
> orderbook-svc) to demonstrate the over-selling race condition. That scope
> expanded the protocol surface (a second implementation, a second test
> stack) and didn't address the bigger gap: **we have no visible evidence
> the 2PC coordinator actually behaves correctly from the end-user's seat.**
>
> This plan is narrower and more useful: a single **AvaloniaUI desktop
> client** that talks to the existing Go coordinator + the existing
> in-tree Go participant (`cmd/participant`) and lets you *see* the
> 2-phase commit lifecycle in real time. It proves the backend works
> without writing a single line of new protocol code.

---

## 1. Goal

Ship a single-user, cross-platform **.NET 8 / AvaloniaUI 11** desktop
client that lets an end-user submit "buy N shares of TICKER" orders
against the existing 2PC coordinator and observe — visually, in the
UI, in real time — the prepare / commit / abort lifecycle that the
coordinator drives.

The app is the **end-user-facing proof** of the coordinator's
correctness. It exercises the same code paths the HTMX web UI exposes
plus the `?sync=true` blocking flow, the topic-dedup behavior, and
the failure scenarios (vote-no, abort, recovery). It also drives the
**race-condition demonstration** the previous plan cared about: open
two windows on the same machine, both try to buy the same scarce
security, exactly one wins.

### Why AvaloniaUI

| Why | Reason |
|---|---|
| Cross-platform from one codebase | Windows / macOS / Linux desktop; the team runs all three. |
| XAML + MVVM | Familiar to anyone who touched WPF / UWP; the model is clear and testable. |
| No web stack in the desktop app | Talks to the coordinator over `HttpClient`; no embedded browser, no JS. |
| Runs without the coordinator being world-reachable | Local-first; bound to `http://localhost:8080` by default with a settings dialog for any URL. |

### Non-goals

- Not a participant service. The Go sample participant (`cmd/participant`) is the only participant in the system. The C# code talks to the coordinator, not to a participant directly.
- Not a production trading app. No order routing, no FIX, no auth, no persistence.
- Not a stress test. The Go integration tests (`internal/coord/integration_test.go`) already cover 6/6 scenarios. The Avalonia app is a **visual** proof, not a new test surface.
- Not a cross-language participant port. The previous "C# accounts-svc / Python orderbook-svc" plan is shelved — see §11 for why.

---

## 2. End-User Scenario (what the app actually does)

The app is a **trading floor simulator** for a single security ("DEMO" —
a synthetic ticker, no real market). The user can:

1. **Place a buy/sell order.** The UI submits a 2PC transaction with two
   participants: `accounts-svc` (validates cash balance) and
   `inventory-svc` (validates share availability). The order is the
   business intent; the two participants are the system-of-record.
2. **Watch the lifecycle.** The detail pane shows the same fields the
   HTMX detail view shows (`id`, `topic_key`, `status`, `votes`,
   `timestamps`) but updates via `HttpClient` polling, with a live
   state-machine badge and a timeline strip.
3. **Run the over-selling demo.** The user opens a second window of the
   same app (or a second "tab" inside one window), picks the same
   ticker, submits a buy that would exceed available quantity, and
   sees exactly one of the two transactions commit while the other
   aborts with reason `insufficient funds` / `out of stock`.
4. **Trigger failure scenarios on demand.** A "Dev tools" panel lets
   the user set a "vote-no probability" on the next prepare (by
   talking to a side-channel the Go participant exposes, or by
   submitting a deliberately-over-budget order), cancel an in-flight
   transaction, or restart the coordinator and watch recovery
   converge the in-flight tx.

### Mental model the user gets

- "I clicked Buy. Two things happened in the background: my cash was
  checked, and the share inventory was checked. If both said yes, the
  order went through. If either said no, nothing changed."
- "The race condition isn't a server bug — it's a serialization rule.
  Two clicks don't both win."

---

## 3. UI Layout

A single window with three panes, plus a settings drawer.

```
┌────────────────────────────────────────────────────────────────────────┐
│ TradingClient  [Settings]  [Dev tools]  [About]              localhost:8080 │
├────────────────┬───────────────────────────────────────────────────────┤
│ ORDERS         │  ORDER DETAIL                                          │
│                │                                                        │
│ ▶ tx_demo-...  │  topic_key:  buy-AAPL-100                             │
│   PREPARING    │  status:     ● PREPARING                              │
│   2.4s ago     │                                                        │
│                │  TIMELINE                                              │
│ ✓ tx_buy-...   │   17:42:01  INIT                                       │
│   COMMITTED    │   17:42:01  PREPARING  ─┐                              │
│                │   17:42:02  COMMITTING   │  ~1.1s                     │
│ ✗ tx_sell-...  │   17:42:02  COMMITTED    ─┘                            │
│   ABORTED      │                                                        │
│   "out of      │  PARTICIPANTS                                          │
│    stock"      │   accounts-svc     yes → committed   0.4s              │
│                │   inventory-svc    yes → committed   0.4s              │
│ [Place Order]  │                                                        │
│                │  RAW JSON                                              │
│ [Race Demo]    │   { ... }                                              │
└────────────────┴───────────────────────────────────────────────────────┘
```

### Panes

| Pane | Behavior |
|---|---|
| **Orders list** (left) | Polls `GET /transactions?limit=20` every 2s. Each row is a transaction: status badge, topic key, age. Click → detail. |
| **Order detail** (right) | Polls `GET /transactions/{id}` every 1s. Shows status badge, timeline, per-participant vote + outcome, raw JSON. |
| **Place Order form** | Modal. Fields: ticker (dropdown of configured tickers), side (BUY/SELL), qty, price, user_id. Submits to `POST /transactions` (default async; checkbox for `?sync=true`). |
| **Race Demo** | Modal. Two side-by-side forms pre-filled with the same scarce security; a single "Start both" button fires both transactions within 50ms of each other so the conflict window is wide. |
| **Dev tools drawer** | Settings: coordinator URL, poll interval, log verbosity. Actions: cancel selected tx, view coordinator log lines, "kick recovery" (calls `POST /__internal/recover` if we add one, or just restarts the coordinator via a configurable shell command). |

### Status badge colors

- `INIT` / `PREPARING` — blue, pulsing
- `COMMITTING` — yellow
- `COMMITTED` — green
- `ABORTING` / `ABORTED` — red, with reason tooltip
- `FAILED` — dark red, with reason

### Timeline

A horizontal strip: 4 nodes (INIT, PREPARING, COMMITTING, COMMITTED/ABORTED) drawn left-to-right. Each node fills in when the corresponding state transition is observed in the polled JSON. Tooltips on each node show the timestamp.

---

## 4. Architecture

```
            ┌─────────────────────────────────────────────┐
            │  AvaloniaUI 11 (single-process desktop)      │
            │                                              │
            │  ┌─────────┐   ┌──────────────┐              │
            │  │ Views   │◄──┤ ViewModels   │              │
            │  │ (XAML)  │   │ (Community-  │              │
            │  └─────────┘   │  Toolkit.Mvvm)│              │
            │        ▲       └──────┬───────┘              │
            │        │              │                       │
            │  ┌─────┴──────────────▼──────────┐           │
            │  │ CoordinatorClient (HttpClient)│           │
            │  │  - ListTransactions            │           │
            │  │  - GetTransaction              │           │
            │  │  - CreateTransaction           │           │
            │  │  - AddParticipant              │           │
            │  │  - CancelTransaction           │           │
            │  └──────────────┬─────────────────┘           │
            └─────────────────┼───────────────────────────┘
                              │ HTTP/JSON
                              ▼
                ┌─────────────────────────────┐
                │  2PC Coordinator (Go)       │
                │  http://localhost:8080      │
                └────────────┬────────────────┘
                             │ HTTP/JSON
              ┌──────────────┴──────────────┐
              ▼                             ▼
     accounts-svc (Go)            inventory-svc (Go)
     :8081                        :8082
     action: authorize            action: reserve
```

### Project structure

```
clients/avalonia/
├── TradingClient.sln
├── src/
│   └── TradingClient/
│       ├── TradingClient.csproj
│       ├── App.axaml
│       ├── App.axaml.cs
│       ├── Program.cs
│       ├── Models/
│       │   ├── Transaction.cs        # mirrors /transactions/{id} JSON
│       │   ├── TransactionStatus.cs  # enum + display attributes
│       │   ├── Participant.cs        # vote/final outcome
│       │   └── OrderRequest.cs       # form-bound DTO
│       ├── Services/
│       │   ├── ICoordinatorClient.cs
│       │   ├── CoordinatorClient.cs  # HttpClient wrapper
│       │   ├── PollingService.cs     # IHostedService, polls on a timer
│       │   └── OrderSubmissionService.cs
│       ├── ViewModels/
│       │   ├── MainWindowViewModel.cs
│       │   ├── OrderListViewModel.cs
│       │   ├── OrderDetailViewModel.cs
│       │   ├── PlaceOrderViewModel.cs
│       │   ├── RaceDemoViewModel.cs
│       │   └── DevToolsViewModel.cs
│       └── Views/
│           ├── MainWindow.axaml
│           ├── OrderListView.axaml
│           ├── OrderDetailView.axaml
│           ├── PlaceOrderView.axaml
│           ├── RaceDemoView.axaml
│           └── DevToolsView.axaml
└── tests/
    └── TradingClient.Tests/
        ├── CoordinatorClientTests.cs    # uses a fake HttpMessageHandler
        └── ViewModelTests.cs
```

### .NET packages

| Package | Version | Why |
|---|---|---|
| `Avalonia` | 11.2.* | UI framework |
| `Avalonia.Desktop` | 11.2.* | Desktop integration |
| `Avalonia.Themes.Fluent` | 11.2.* | Fluent theme |
| `CommunityToolkit.Mvvm` | 8.4.* | `[ObservableProperty]`, `[RelayCommand]`, source generators |
| `Microsoft.Extensions.Hosting` | 8.* | `IHostedService` for the polling background loop |
| `Microsoft.Extensions.Http` | 8.* | Typed `HttpClient` via DI |
| `System.Text.Json` | (built-in) | JSON |
| `xunit` + `xunit.runner.visualstudio` | latest | unit tests |
| `Microsoft.NET.Test.Sdk` | latest | test runner |

No `Newtonsoft.Json`, no `RestSharp` — `HttpClient` + `System.Text.Json` is enough and one less dependency.

---

## 5. Talking to the coordinator

All HTTP. The C# client is a thin wrapper; no business logic.

### Endpoints used

| Endpoint | Used for | Notes |
|---|---|---|
| `GET /healthz` | Startup check; status bar | Should be 200 before the app enables the "Place Order" button |
| `GET /readyz` | Startup check | 503 → show a banner "coordinator not ready" |
| `GET /transactions?limit=N` | Orders list polling | Polled every 2s; cancelled when window closes |
| `GET /transactions/{id}` | Detail pane polling | Polled every 1s while the detail is visible |
| `POST /transactions` | Submit a new order | Body matches `CreateTransactionRequest` in `internal/api/types.go` |
| `POST /transactions?sync=true` | Same, blocking | Used in Race Demo to force the user to wait for the result |
| `POST /transactions/{id}/participants` | (Optional) Add a participant | For showing the AddParticipant UI; not required for v1 |
| `POST /transactions/{id}/cancel` | Cancel from Dev tools | Lets the user demonstrate cancel-while-preparing |
| `GET /metrics` | (Optional) Health detail | Surface Prometheus counters in the Dev tools drawer |

### Request body for "Place Order"

The C# model mirrors `internal/api/types.go` `CreateTransactionRequest`:

```json
{
  "topic_key": "buy-AAPL-100-2026-10-18T17-42-01Z",
  "timeout_ms": 10000,
  "operations": [
    {
      "participant": "accounts-svc",
      "action": "authorize",
      "params": { "account": "A", "amount": 1500.00 }
    },
    {
      "participant": "inventory-svc",
      "action": "reserve",
      "params": { "item": "AAPL", "qty": 100 }
    }
  ]
}
```

`topic_key` is generated client-side as `buy-{ticker}-{qty}-{ISO-timestamp}-{short-guid}` so the in-flight dedup behaviour is visible: pressing Buy twice in quick succession produces the same `transaction_id` and the user can see the "second click was a dedup" toast.

### Response handling

- `202 Accepted` → open detail pane, start polling
- `200 OK` (sync mode) → land on detail pane at terminal state, no polling
- `408 Request Timeout` → detail pane shows "synced request timed out" badge
- `409 Conflict` (topic key dedup on terminal — actually a new tx, not a conflict) → surface as a fresh `transaction_id` and continue
- `422 Unprocessable Entity` → detail pane in ABORTED state, with the reason
- Network errors → red banner "coordinator unreachable", auto-retry with backoff

### CoordinatorClient.cs shape

```csharp
public interface ICoordinatorClient
{
    Task<bool> IsHealthyAsync(CancellationToken ct = default);
    Task<IReadOnlyList<Transaction>> ListTransactionsAsync(int limit, CancellationToken ct = default);
    Task<Transaction?> GetTransactionAsync(string id, CancellationToken ct = default);
    Task<CreateTransactionResult> CreateTransactionAsync(CreateTransactionRequest req, bool sync = false, CancellationToken ct = default);
    Task<Transaction?> AddParticipantAsync(string id, AddParticipantRequest req, CancellationToken ct = default);
    Task<bool> CancelTransactionAsync(string id, CancellationToken ct = default);
}

public sealed record CreateTransactionResult(
    string Id,
    HttpStatusCode StatusCode,
    string? ErrorMessage);

public sealed class CoordinatorClient : ICoordinatorClient { /* HttpClient impl */ }
```

Registered in DI as a **typed** `HttpClient` so the framework manages the `HttpMessageHandler` lifetime and DNS refresh:

```csharp
services.AddHttpClient<ICoordinatorClient, CoordinatorClient>(c =>
{
    c.BaseAddress = new Uri(coordinatorUrl);
    c.Timeout = TimeSpan.FromSeconds(15);
});
```

---

## 6. The Race Demo (the killer feature)

The previous plan's whole point was to *prove* the over-selling race is
prevented. The Avalonia app makes that proof **visible**.

### How it works

1. User clicks "Race Demo" in the toolbar.
2. Modal opens with two side-by-side order forms, both pre-filled with
   the same ticker (`AAPL`), same qty (`100`), same price. Account A
   has `1500` cash, security `AAPL` has `100` shares.
3. User clicks "Start both" once. The ViewModel fires both
   `CreateTransactionAsync` calls within 50ms of each other (two
   `Task.Run` continuations on the UI dispatcher).
4. Each side of the modal starts polling its own transaction's
   detail at 500ms intervals.
5. Exactly one of the two transactions reaches `COMMITTED`; the
   other reaches `ABORTED` with reason `insufficient funds` (or
   `out of stock`, depending on which check the participant runs
   first). The UI makes the winner obvious — green check on one
   side, red X with reason on the other.

### Implementation notes

- **Two coordinators? No.** Both transactions go to the same
  coordinator. The race is on the participant's read-modify-write of
  the inventory row.
- **Why 50ms apart?** Closer than the participant's prepare handler
  (`time.Sleep(50ms)` then check) is long enough to make the prepare
  windows overlap deterministically. The Go participant's prepare
  takes ~50ms; the second tx starts preparing before the first tx
  finishes, so both are inside Phase 1 at the same time, both see
  `available_qty=100` and `balance=1500`, both try to vote yes. The
  one whose prepare writes the `write_locks` row first wins; the other
  finds the lock present and votes no.
- **Re-runnable.** After both transactions are terminal, the demo
  modal resets: it tops up `AAPL` back to 100 and `A`'s balance to
  1500 (via a "Reset demo data" button that calls a side-channel
  endpoint on the Go participant, see §8).
- **Deterministic but truthful.** The demo doesn't *fake* the race
  outcome — it observes the coordinator's actual decision and renders
  it. If the coordinator ever fails to prevent the over-sell, the UI
  shows two green checkmarks and the demo is failing, which is exactly
  what a manual regression test should catch.

---

## 7. Configuration & persistence

The app needs to remember the coordinator URL between launches and
let the user change it.

- `appsettings.json` for the default URL (`http://localhost:8080`)
- `AppData/Roaming/TradingClient/settings.json` (or XDG on Linux) for the
  per-user override
- Settings dialog is one window: textbox for URL, slider for poll
  interval, "Test connection" button that calls `/healthz` and shows
  a green/red indicator

### Sample `settings.json`

```json
{
  "coordinatorUrl": "http://localhost:8080",
  "listPollIntervalMs": 2000,
  "detailPollIntervalMs": 1000,
  "defaultTimeoutMs": 10000,
  "theme": "FluentDark"
}
```

---

## 8. Side-channel: resetting demo data

The Go participant (`cmd/participant`) currently doesn't have a
"reset to initial state" endpoint, but the demo needs one to be
re-runnable. **Two options, in preference order:**

### Option A (preferred): a tiny admin endpoint on the Go participant

Add to `internal/pax/server.go`:

```go
r.Post("/admin/reset", s.HandleReset)
```

```go
func (s *ParticipantServer) HandleReset(w http.ResponseWriter, r *http.Request) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.ledger = map[string]int64{"A": 10000, "B": 10000}
    s.securities = map[string]int{"AAPL": 100, "MSFT": 500}
    s.prepared = map[string]bool{}
    s.txStates = map[string]string{}
    s.pendingDeltas = map[string]map[string]int64{}
    w.WriteHeader(http.StatusOK)
}
```

This is a 20-line change to the Go participant. It is a **dev-only**
endpoint; in production it would be guarded by auth. The Avalonia
app's "Reset demo data" button calls it.

### Option B: restart the participant process

The Dev tools drawer has a "Restart participant" button that runs a
configurable shell command (e.g.,
`make restart-participant`). Slower and noisier, but zero code
change on the Go side.

**Go with Option A first.** Fall back to Option B only if there's
resistance to adding the endpoint.

---

## 9. MVVM wiring (the parts that matter)

The polling service runs as `IHostedService`, so the ViewModels can
subscribe to it via `IObservable<Transaction[]>` and
`IObservable<Transaction>`. This decouples the poll cadence from the
UI thread.

```csharp
public sealed class PollingService : BackgroundService
{
    private readonly ICoordinatorClient _client;
    private readonly Subject<IReadOnlyList<Transaction>> _list = new();
    private readonly Subject<Transaction> _detail = new();
    public IObservable<IReadOnlyList<Transaction>> ListStream => _list;
    public IObservable<Transaction> DetailStream => _detail;
    public void Watch(string id) { /* register id for detail polling */ }
    public void Unwatch(string id) { /* remove id */ }

    protected override async Task ExecuteAsync(CancellationToken ct)
    {
        while (!ct.IsCancellationRequested)
        {
            try
            {
                _list.OnNext(await _client.ListTransactionsAsync(20, ct));
                // for each watched id, push the latest
                foreach (var id in _watchedIds)
                {
                    var t = await _client.GetTransactionAsync(id, ct);
                    if (t != null) _detail.OnNext(t);
                }
            }
            catch (Exception ex) { _logger.LogWarning(ex, "poll failed"); }
            await Task.Delay(_interval, ct);
        }
    }
}
```

ViewModels subscribe via `System.Reactive` (already pulled in
transitively by `CommunityToolkit.Mvvm`) and dispatch back to the UI
thread via `Avalonia.Threading.Dispatcher.UIThread.Post`.

---

## 10. Acceptance criteria for v1 of the Avalonia client

- [ ] `dotnet build` succeeds on .NET 8 SDK.
- [ ] `dotnet run` from `clients/avalonia/` starts the app on Windows, macOS, Linux.
- [ ] On startup, the app shows a red banner if `GET /healthz` is not 200, and a green one when it is. "Place Order" is disabled until the coordinator is reachable.
- [ ] The orders list polls `GET /transactions?limit=20` every 2s and updates without UI flicker (diff-based binding).
- [ ] Clicking an order shows its detail pane, which polls `GET /transactions/{id}` every 1s and updates the status badge, timeline, and participants table.
- [ ] "Place Order" form submits a valid transaction and lands on the detail pane at `PREPARING`.
- [ ] `?sync=true` checkbox blocks the form until the transaction is terminal; the detail pane opens at the terminal state.
- [ ] Double-clicking "Place Order" within the prepare window shows the same `transaction_id` (topic-dedup toast).
- [ ] "Race Demo" modal: two simultaneous buy orders on the same scarce security, one commits, one aborts, with a reason. The result is rendered within 2s of both submissions.
- [ ] "Reset demo data" calls the participant admin endpoint and returns the inventory/balance to the initial state.
- [ ] "Cancel" button in the detail pane calls `POST /transactions/{id}/cancel`; the status badge transitions PREPARING → ABORTING → ABORTED.
- [ ] Unit tests cover the `CoordinatorClient` (using a fake `HttpMessageHandler`) and the `OrderListViewModel` (using a fake `ICoordinatorClient`).
- [ ] README at `clients/avalonia/README.md` documents: how to build, how to run, how to demo the race, screenshots of each major screen.

---

## 11. Why this plan supersedes the previous one

The previous `clients/PLAN.md` (2026-09-17) called for:

| Old plan element | Why it's superseded |
|---|---|
| **C# `accounts-svc` participant** | We already have a Go participant (`cmd/participant`) that handles all 3 actions. A C# participant doubles the protocol surface area for no end-user-visible benefit. The Avalonia app proves the backend; it doesn't need a second participant implementation. |
| **Python `orderbook-svc` participant** | Same reason. And Python is the wrong language to introduce when the rest of the system is Go + C#. |
| **Cross-language shadow-write proof** | The race demo in the Avalonia app is a stronger proof: an end-user actually clicks two buttons and watches one win. No `FOR UPDATE` simulation can show the same thing. |
| **`clients/PLAN.md` design doc only** | Plans are cheap, working software is expensive. This plan includes an actual project skeleton, the DI wiring, the polling service, and the acceptance criteria — closeable into a working app, not an open design exercise. |
| **No UI for v1** | The HTMX web UI exists, but it doesn't demonstrate the over-selling race visually. The Avalonia app does. |

If a future need really does call for a C# or Python participant (e.g., a partner integrating with a real order-book system that has to speak 2PC in their own language), the design pattern from this plan — a typed `HttpClient` wrapper, the `CoordinatorClient` interface — gives them a starting point. But the partner's participant would replace the Go sample participant for that one integration; the Avalonia app would talk to a different coordinator (or to a coordinator registered with both).

---

## 12. Implementation order

1. **Skeleton + connectivity** (½ day)
   - `dotnet new sln`, `dotnet new classlib` for `TradingClient`, set up `App.axaml`, `Program.cs`, `CoordinatorClient` (typed `HttpClient`), `/healthz` check on startup.
2. **Models + JSON** (½ day)
   - `Transaction`, `Vote`, `CreateTransactionRequest` as C# records with `System.Text.Json` attributes. Match the Go `internal/api/types.go` shapes byte-for-byte (or property-for-property).
3. **Polling service** (½ day)
   - `PollingService` as `BackgroundService`, `Subject<T>`-based streams, ViewModel subscription. End-to-end test: orders list updates every 2s.
4. **Order list + detail panes** (1 day)
   - XAML, ViewModels, status badges, timeline. Manual smoke test against a running coordinator.
5. **Place Order form** (½ day)
   - Modal, form binding, async + sync submit, dedup toast on double-click.
6. **Race Demo** (1 day)
   - Two side-by-side forms, 50ms-spaced dispatch, side-by-side polling, "winner / loser" rendering.
7. **Reset demo data** (½ day, after the Go side adds the admin endpoint)
   - Calls `POST /admin/reset` on the participant; verifies the inventory/balance are back to initial state.
8. **Dev tools drawer** (½ day)
   - Settings (URL, poll interval, theme), cancel-selected action, log viewer.
9. **Unit tests** (½ day)
   - `CoordinatorClientTests` (fake `HttpMessageHandler` returning canned JSON), `ViewModelTests` (fake `ICoordinatorClient`).
10. **README + screenshots** (½ day)
    - Build instructions, run instructions, race demo walkthrough, three screenshots (orders list, detail, race result).

**Total estimate:** 5 working days, end-to-end. The Avalonia app is a lot of XAML, but the Go side doesn't change at all (except the 20-line admin endpoint in §8).

---

## 13. Risks & open questions

| Risk | Mitigation |
|---|---|
| Avalonia XAML hot-reload in CI is finicky | Don't depend on it; build once, run once, manual smoke. |
| Long-running polls leak `HttpClient` sockets | Use `IHttpClientFactory` (typed client), not `new HttpClient()`. |
| `System.Text.Json` doesn't deserialize snake_case by default | Use `JsonSerializerOptions { PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower }` in the typed HttpClient. The Go side already emits snake_case. |
| The "race" demo isn't actually racy enough on a fast machine | Wrap both submits in `Task.Run`; if both succeed faster than the prepare window, slow the demo participant to 100ms prepare delay via env var. |
| User runs the demo against a non-demo coordinator | Show a big yellow "DEMO MODE" banner whenever the coordinator URL is `localhost:*`. |
| Avalonia 11.2 vs 11.3 API drift | Pin `<PackageReference Include="Avalonia" Version="11.2.*" />` exactly; don't float. |

---

## 14. Out of scope (defer to v2 of the client)

- Authenticated coordinator (the Go side doesn't have auth either).
- Multiple coordinators / failover.
- WebSocket / Server-Sent Events for push updates (still poll).
- Charts of historical transactions.
- A "participant health" view (ping each participant directly).
- i18n (English only).
- Code-signed installers.

---

*End of plan. Once approved, the first commit is `dotnet new sln` in
`clients/avalonia/` and a 20-line `POST /admin/reset` on the Go
participant.*
