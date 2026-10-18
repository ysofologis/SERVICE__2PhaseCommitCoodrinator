# TradingClient — AvaloniaUI 2PC Trading Client

A cross-platform .NET 8 desktop app that drives the Go 2PC coordinator
and proves its correctness **from the end-user's seat**.

See [`../PLAN.md`](../PLAN.md) for the full design rationale.

## What it does

- **Orders list (left pane)** — polls `GET /transactions?limit=25` every
  2s, shows recent transactions with status badges.
- **Order detail (right pane)** — polls `GET /transactions/{id}` every
  1s; shows status, lifecycle timeline, per-participant vote/outcome,
  and any error message.
- **Place Order** — form posts a buy/sell with two participants
  (`accounts-svc` for cash, `inventory-svc` for shares) to
  `POST /transactions`. Optional `?sync=true` blocks until terminal.
- **Race Demo** — fires two simultaneous buy orders on the same scarce
  security ~50ms apart. Renders which one committed and which aborted.
  If both commit, the demo reports "OVER-SELL" — the demo is a manual
  regression test.
- **Dev tools** — health check, settings drawer.

## Build

```bash
cd clients/avalonia
dotnet build
```

## Run

Start the Go coordinator + 2 participants first:

```bash
cd /path/to/2PhaseCommitAPI
make run            # brings up :8080 (coordinator) + :8081/:8082 (participants)
```

Then the Avalonia app:

```bash
cd clients/avalonia
dotnet run --project src/TradingClient
```

## Run the race demo

1. Make sure the participant has fresh state: (no `POST /admin/reset`
   endpoint yet — restart the participant process for now).
2. Click "Race demo" in the bottom action bar.
3. Click "Start both".
4. One side should show `COMMITTED` (green), the other `ABORTED` (red)
   with a reason. The bottom verdict reads
   `✓ Race prevented: exactly one commit, one abort.`

If both come back `COMMITTED`, **the coordinator has a bug** — file an
issue.

## Tests

```bash
cd clients/avalonia
dotnet test
```

Unit tests cover:

- `CoordinatorClient` HTTP shape (using a stub `HttpMessageHandler`)
- `RaceDemoViewModel` end-to-end (committed + aborted case, and
  over-sell detection)

## Project layout

```
avalonia/
├── TradingClient.sln
├── global.json                # pins .NET SDK to 8.0.x
├── src/TradingClient/
│   ├── TradingClient.csproj   # Avalonia 11.2 + CommunityToolkit.Mvvm 8.4
│   ├── Program.cs             # entry point, DI host, Avalonia app builder
│   ├── App.axaml(.cs)         # ApplicationDefinition, FluentTheme
│   ├── ServiceCollectionExtensions.cs  # AddTradingClient(coordinatorUrl)
│   ├── Models/                # wire models (Transaction, Participant, etc.)
│   ├── Services/
│   │   ├── ICoordinatorClient.cs
│   │   ├── CoordinatorClient.cs   # typed HttpClient wrapper
│   │   └── PollingService.cs      # IHostedService, ListStream + DetailStream
│   ├── ViewModels/
│   │   ├── MainWindowViewModel.cs
│   │   ├── OrderListViewModel.cs
│   │   ├── OrderDetailViewModel.cs
│   │   ├── PlaceOrderViewModel.cs
│   │   ├── RaceDemoViewModel.cs
│   │   └── DevToolsViewModel.cs
│   └── Views/
│       ├── MainWindow.axaml(.cs)
│       ├── OrderListView.axaml(.cs)
│       ├── OrderDetailView.axaml(.cs)
│       ├── PlaceOrderWindow.axaml(.cs)
│       ├── RaceDemoWindow.axaml(.cs)
│       └── Converters.cs
└── tests/TradingClient.Tests/
    ├── CoordinatorClientTests.cs
    └── RaceDemoViewModelTests.cs
```

## Configuration

The default coordinator URL is `http://localhost:8080`. To change it,
edit `Program.cs` or set the `Coordinator__Url` environment variable
before launching:

```bash
Coordinator__Url=http://192.168.1.42:8080 dotnet run --project src/TradingClient
```
