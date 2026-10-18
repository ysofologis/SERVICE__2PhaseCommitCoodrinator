using System;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Microsoft.Extensions.Logging;
using TradingClient.Models;
using TradingClient.Services;

namespace TradingClient.ViewModels;

/// <summary>
/// Backs the "Race Demo" modal. Fires two transactions ~50ms apart
/// against the same scarce security and renders which one committed
/// and which one aborted. This is the visual proof the coordinator
/// prevents the over-selling race condition.
///
/// The participants are real (the Go sample participant holds ledger
/// state in memory). Account "A" starts with 1500 cash, security "AAPL"
/// starts with 100 shares — exactly enough for one buy of 100 @ $15.
/// Whichever transaction the participant accepts first wins.
/// </summary>
public sealed partial class RaceDemoViewModel : ViewModelBase
{
    private readonly ICoordinatorClient _client;
    private readonly ILogger<RaceDemoViewModel> _log;

    [ObservableProperty] private int _quantity = 100;
    [ObservableProperty] private decimal _price = 15.00m;
    [ObservableProperty] private string _ticker = "AAPL";
    [ObservableProperty] private string _account = "A";

    [ObservableProperty] private bool _isRunning;
    [ObservableProperty] private string? _resultA;
    [ObservableProperty] private string? _resultB;
    [ObservableProperty] private string? _winnerLabel;

    public event Action<string>? TransactionSubmitted;

    public RaceDemoViewModel(ICoordinatorClient client, ILogger<RaceDemoViewModel> log)
    {
        _client = client;
        _log = log;
    }

    [RelayCommand(CanExecute = nameof(CanRun))]
    private async Task RunAsync()
    {
        if (IsRunning) return;
        IsRunning = true;
        ResultA = null;
        ResultB = null;
        WinnerLabel = null;
        try
        {
            // Build two requests with distinct topic keys so the
            // coordinator doesn't dedup them. Both want the same scarce
            // resource.
            var amount = (double)(Quantity * Price);
            var reqA = new CreateTransactionRequest(
                TopicKey: $"race-A-{Ticker}-{Quantity}-{Guid.NewGuid():N}",
                TimeoutMs: 5000,
                Operations: new System.Collections.Generic.List<Operation>
                {
                    new("accounts-svc", "authorize",
                        new System.Collections.Generic.Dictionary<string, object>
                        {
                            ["account"] = Account, ["amount"] = amount,
                        }),
                    new("inventory-svc", "reserve",
                        new System.Collections.Generic.Dictionary<string, object>
                        {
                            ["item"] = Ticker, ["qty"] = Quantity,
                        }),
                });
            var reqB = new CreateTransactionRequest(
                TopicKey: $"race-B-{Ticker}-{Quantity}-{Guid.NewGuid():N}",
                TimeoutMs: 5000,
                Operations: new System.Collections.Generic.List<Operation>
                {
                    new("accounts-svc", "authorize",
                        new System.Collections.Generic.Dictionary<string, object>
                        {
                            ["account"] = Account, ["amount"] = amount,
                        }),
                    new("inventory-svc", "reserve",
                        new System.Collections.Generic.Dictionary<string, object>
                        {
                            ["item"] = Ticker, ["qty"] = Quantity,
                        }),
                });

            // Use sync mode so we wait for the terminal state of each
            // transaction before reporting the result.
            var taskA = Task.Run(() => _client.CreateTransactionAsync(reqA, sync: true));
            // ~50ms gap so the second prepare window starts inside the
            // first transaction's prepare window — this is what makes
            // the race observable.
            await Task.Delay(50);
            var taskB = Task.Run(() => _client.CreateTransactionAsync(reqB, sync: true));

            var results = await Task.WhenAll(taskA, taskB);

            ResultA = FormatResult("A", results[0]);
            ResultB = FormatResult("B", results[1]);

            // Render the winner/loser verdict.
            var committed = 0;
            var aborted = 0;
            if (results[0].Status == TransactionStatus.Committed) committed++;
            else if (results[0].Status == TransactionStatus.Aborted) aborted++;
            if (results[1].Status == TransactionStatus.Committed) committed++;
            else if (results[1].Status == TransactionStatus.Aborted) aborted++;

            if (committed == 1 && aborted == 1)
                WinnerLabel = "✓ Race prevented: exactly one commit, one abort. Coordinator wins.";
            else if (committed == 2)
                WinnerLabel = "✗ OVER-SELL: both transactions committed. Coordinator has a bug.";
            else
                WinnerLabel = "? Both transactions ended in the same state. Race outcome unclear.";

            if (results[0].Id is not null) TransactionSubmitted?.Invoke(results[0].Id);
            if (results[1].Id is not null) TransactionSubmitted?.Invoke(results[1].Id);
        }
        catch (Exception ex)
        {
            _log.LogWarning(ex, "race demo failed");
            WinnerLabel = $"✗ demo failed: {ex.Message}";
        }
        finally
        {
            IsRunning = false;
        }
    }

    private bool CanRun() => !IsRunning && Quantity > 0;

    private static string FormatResult(string label, CreateTransactionResult r)
    {
        if (r.Status != TransactionStatus.Unknown)
        {
            // We have a known terminal state, even if the request itself
            // returned a non-2xx (e.g., 422 for a vote-no abort). Show
            // the status with the optional reason.
            var reason = string.IsNullOrEmpty(r.ErrorMessage) ? "" : $" ({r.ErrorMessage})";
            return $"{label}: {r.Id ?? "?"} → {r.Status.DisplayName()}{reason}";
        }
        if (!r.Success) return $"{label}: ✗ request failed: {r.ErrorMessage}";
        return $"{label}: {r.Id} → {r.Status.DisplayName()}";
    }

    partial void OnQuantityChanged(int value) => RunCommand.NotifyCanExecuteChanged();
    partial void OnIsRunningChanged(bool value) => RunCommand.NotifyCanExecuteChanged();
}
