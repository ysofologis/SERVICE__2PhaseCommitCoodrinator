using System;
using System.Collections.ObjectModel;
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
/// Backs the left-hand "Orders" pane. Subscribes to
/// <see cref="PollingService.ListStream"/> and rebroadcasts as an
/// observable collection that the View binds to.
/// </summary>
public sealed partial class OrderListViewModel : ViewModelBase
{
    private readonly PollingService _poller;
    private readonly ILogger<OrderListViewModel> _log;

    [ObservableProperty]
    private ObservableCollection<Transaction> _orders = new();

    [ObservableProperty]
    private Transaction? _selectedOrder;

    public OrderListViewModel(PollingService poller, ILogger<OrderListViewModel> log)
    {
        _poller = poller;
        _log = log;
        // Note: we're subscribing on whatever thread the VM is constructed
        // on (UI thread). The PollingService already returns empty lists
        // on transport errors, so we just diff-and-replace here.
        _poller.ListStream
            .ObserveOn(AvaloniaScheduler.Instance)
            .Subscribe(OnListReceived, ex => _log.LogWarning(ex, "list stream error"));
    }

    private void OnListReceived(System.Collections.Generic.IReadOnlyList<Transaction> latest)
    {
        // Simple replace. Avalonia's ObservableCollection is good enough
        // for a list of <30 items; a diff would be premature optimization.
        Orders.Clear();
        foreach (var t in latest) Orders.Add(t);
    }

    /// <summary>
    /// Called by the View when the user clicks a row. We notify the
    /// detail VM via a static event so we don't need a back-reference
    /// to it from here (the main window wires them up at construction).
    /// </summary>
    public event Action<Transaction?>? SelectionChanged;

    partial void OnSelectedOrderChanged(Transaction? value)
    {
        if (value is not null) _poller.Watch(value.Id);
        SelectionChanged?.Invoke(value);
    }
}
