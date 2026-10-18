using System;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Microsoft.Extensions.Logging;
using TradingClient.Services;

namespace TradingClient.ViewModels;

/// <summary>
/// Top-level ViewModel for the main window. Holds references to the
/// three panes (orders list, detail, place-order) and the race demo
/// and dev tools. Owns the coordinator health check that gates the
/// "Place Order" button.
/// </summary>
public sealed partial class MainWindowViewModel : ViewModelBase
{
    private readonly ICoordinatorClient _client;
    private readonly ILogger<MainWindowViewModel> _log;

    [ObservableProperty] private bool _isCoordinatorUp;
    [ObservableProperty] private string? _statusBarMessage;

    public OrderListViewModel OrderList { get; }
    public OrderDetailViewModel OrderDetail { get; }
    public PlaceOrderViewModel PlaceOrder { get; }
    public RaceDemoViewModel RaceDemo { get; }
    public DevToolsViewModel DevTools { get; }

    public MainWindowViewModel(
        ICoordinatorClient client,
        ILogger<MainWindowViewModel> log,
        OrderListViewModel orderList,
        OrderDetailViewModel orderDetail,
        PlaceOrderViewModel placeOrder,
        RaceDemoViewModel raceDemo,
        DevToolsViewModel devTools)
    {
        _client = client;
        _log = log;

        OrderList = orderList;
        OrderDetail = orderDetail;
        PlaceOrder = placeOrder;
        RaceDemo = raceDemo;
        DevTools = devTools;

        // When the user picks an order in the list, push it into the
        // detail pane.
        OrderList.SelectionChanged += OnSelectionChanged;
        // When a new transaction is submitted, auto-select it so the
        // user sees the lifecycle without an extra click.
        PlaceOrder.TransactionSubmitted += id => OrderDetail.Show(id);
        RaceDemo.TransactionSubmitted += id => OrderDetail.Show(id);
    }

    private void OnSelectionChanged(Models.Transaction? tx)
    {
        if (tx != null) OrderDetail.Show(tx.Id);
        else OrderDetail.Clear();
    }

    /// <summary>
    /// Called once after the window loads. Probes /healthz + /readyz
    /// and toggles the "Place Order" enable state via
    /// <see cref="IsCoordinatorUp"/>.
    /// </summary>
    [RelayCommand]
    public async Task StartupProbeAsync()
    {
        IsCoordinatorUp = await _client.IsHealthyAsync() && await _client.IsReadyAsync();
        StatusBarMessage = IsCoordinatorUp
            ? "✓ connected to coordinator"
            : "⚠ coordinator unreachable — start it with `make run` in the 2PhaseCommitAPI repo";
        _log.LogInformation("startup probe: coordinator up = {Up}", IsCoordinatorUp);
    }
}
