using System;
using System.Reactive.Concurrency;
using System.Reactive.Linq;
using Avalonia.ReactiveUI;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Microsoft.Extensions.Logging;
using TradingClient.Models;
using TradingClient.Services;

namespace TradingClient.ViewModels;

/// <summary>
/// Backs the right-hand "Order detail" pane. Tracks the currently
/// displayed transaction and pushes live updates from
/// <see cref="PollingService.DetailStream"/> into the bound
/// properties.
/// </summary>
public sealed partial class OrderDetailViewModel : ViewModelBase
{
    private readonly PollingService _poller;
    private readonly ICoordinatorClient _client;
    private readonly ILogger<OrderDetailViewModel> _log;

    [ObservableProperty]
    private Transaction? _current;

    [ObservableProperty]
    private bool _isCancelling;

    [ObservableProperty]
    private string? _lastError;

    public OrderDetailViewModel(
        PollingService poller,
        ICoordinatorClient client,
        ILogger<OrderDetailViewModel> log)
    {
        _poller = poller;
        _client = client;
        _log = log;
        _poller.DetailStream
            .Where(t => Current != null && t.Id == Current.Id)
            .ObserveOn(AvaloniaScheduler.Instance)
            .Subscribe(tx => Current = tx);
    }

    public void Show(string id)
    {
        _poller.Watch(id);
        // Force a one-off fetch so the pane populates immediately
        // (don't wait for the next polling tick).
        _ = FetchAsync(id);
    }

    private async System.Threading.Tasks.Task FetchAsync(string id)
    {
        var tx = await _client.GetTransactionAsync(id);
        if (tx != null && Current?.Id == id)
            Avalonia.Threading.Dispatcher.UIThread.Post(() => Current = tx);
    }

    public void Clear()
    {
        if (Current != null) _poller.Unwatch(Current.Id);
        Current = null;
    }

    [RelayCommand(CanExecute = nameof(CanCancel))]
    private async System.Threading.Tasks.Task CancelAsync()
    {
        if (Current is null) return;
        IsCancelling = true;
        LastError = null;
        try
        {
            var ok = await _client.CancelTransactionAsync(Current.Id);
            if (!ok) LastError = "cancel request failed";
        }
        catch (Exception ex)
        {
            _log.LogWarning(ex, "cancel failed");
            LastError = ex.Message;
        }
        finally
        {
            IsCancelling = false;
        }
    }

    private bool CanCancel() => Current is { Status: TransactionStatus.Init or TransactionStatus.Preparing or TransactionStatus.Committing };
}
