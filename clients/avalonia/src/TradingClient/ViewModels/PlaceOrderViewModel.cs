using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Microsoft.Extensions.Logging;
using TradingClient.Models;
using TradingClient.Services;

namespace TradingClient.ViewModels;

/// <summary>
/// Backs the "Place Order" form. Builds a <see cref="CreateTransactionRequest"/>
/// from the form fields, submits it, and reports the result.
/// </summary>
public sealed partial class PlaceOrderViewModel : ViewModelBase
{
    private readonly ICoordinatorClient _client;
    private readonly ILogger<PlaceOrderViewModel> _log;

    [ObservableProperty] private string _ticker = "AAPL";
    [ObservableProperty] private string _side = "BUY";
    [ObservableProperty] private int _quantity = 100;
    [ObservableProperty] private decimal _price = 15.00m;
    [ObservableProperty] private string _account = "A";
    [ObservableProperty] private bool _useSync;
    [ObservableProperty] private int _timeoutMs = 10000;

    [ObservableProperty] private bool _isSubmitting;
    [ObservableProperty] private string? _resultMessage;
    [ObservableProperty] private string? _lastTransactionId;

    public IReadOnlyList<string> Tickers { get; } = new[] { "AAPL", "MSFT", "GOOG" };
    public IReadOnlyList<string> Sides { get; } = new[] { "BUY", "SELL" };

    public event Action<string>? TransactionSubmitted;

    public PlaceOrderViewModel(ICoordinatorClient client, ILogger<PlaceOrderViewModel> log)
    {
        _client = client;
        _log = log;
    }

    [RelayCommand(CanExecute = nameof(CanSubmit))]
    private async Task SubmitAsync()
    {
        if (IsSubmitting) return;
        IsSubmitting = true;
        ResultMessage = null;
        try
        {
            // topic_key is generated client-side so the in-flight dedup
            // behaviour is observable: pressing Submit twice in quick
            // succession produces the same transaction_id and the user
            // sees a "duplicate" toast.
            var topicKey = $"{Side.ToLowerInvariant()}-{Ticker}-{Quantity}-{DateTime.UtcNow:yyyyMMddTHHmmssZ}-{ShortGuid()}";
            var request = BuildRequest(topicKey);

            var result = await _client.CreateTransactionAsync(request, UseSync);
            if (!result.Success || result.Id is null)
            {
                ResultMessage = $"✗ {(result.ErrorMessage ?? $"HTTP {result.StatusCode}")}";
                return;
            }
            LastTransactionId = result.Id;
            ResultMessage = UseSync
                ? $"✓ {result.Id} → {result.Status.DisplayName()}"
                : $"✓ {result.Id} accepted (PREPARING)";
            TransactionSubmitted?.Invoke(result.Id);
        }
        catch (Exception ex)
        {
            _log.LogWarning(ex, "submit failed");
            ResultMessage = $"✗ {ex.Message}";
        }
        finally
        {
            IsSubmitting = false;
        }
    }

    private bool CanSubmit() => !IsSubmitting && !string.IsNullOrWhiteSpace(Ticker) && Quantity > 0;

    private CreateTransactionRequest BuildRequest(string topicKey) => new(
        TopicKey: topicKey,
        TimeoutMs: TimeoutMs,
        Operations: new List<Operation>
        {
            // Operation 1: authorize cash on accounts-svc
            new(
                Participant: "accounts-svc",
                Action: "authorize",
                Params: new Dictionary<string, object>
                {
                    ["account"] = Account,
                    ["amount"] = (double)(Quantity * Price),
                }),
            // Operation 2: reserve inventory on inventory-svc
            new(
                Participant: "inventory-svc",
                Action: "reserve",
                Params: new Dictionary<string, object>
                {
                    ["item"] = Ticker,
                    ["qty"] = Quantity,
                }),
        });

    private static string ShortGuid() => Guid.NewGuid().ToString("N")[..6];

    partial void OnTickerChanged(string value) => SubmitCommand.NotifyCanExecuteChanged();
    partial void OnQuantityChanged(int value) => SubmitCommand.NotifyCanExecuteChanged();
    partial void OnIsSubmittingChanged(bool value) => SubmitCommand.NotifyCanExecuteChanged();
}
