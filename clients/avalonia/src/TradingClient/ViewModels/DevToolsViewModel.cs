using System;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Microsoft.Extensions.Logging;
using TradingClient.Services;

namespace TradingClient.ViewModels;

/// <summary>
/// Backs the Dev tools drawer. Surfaces the coordinator URL, the
/// health status, and a button to call the participant
/// <c>POST /admin/reset</c> endpoint to make the race demo re-runnable.
/// </summary>
public sealed partial class DevToolsViewModel : ViewModelBase
{
    private readonly ICoordinatorClient _client;
    private readonly ILogger<DevToolsViewModel> _log;

    [ObservableProperty] private string _coordinatorUrl = "http://localhost:8080";
    [ObservableProperty] private string _accountsParticipantUrl = "http://localhost:8081";
    [ObservableProperty] private string _inventoryParticipantUrl = "http://localhost:8082";
    [ObservableProperty] private bool _isHealthy;
    [ObservableProperty] private string? _statusMessage;
    [ObservableProperty] private bool _isChecking;
    [ObservableProperty] private bool _isResetting;
    [ObservableProperty] private string? _resetMessage;

    public DevToolsViewModel(ICoordinatorClient client, ILogger<DevToolsViewModel> log)
    {
        _client = client;
        _log = log;
    }

    [RelayCommand]
    public async Task CheckHealthAsync()
    {
        if (IsChecking) return;
        IsChecking = true;
        try
        {
            IsHealthy = await _client.IsHealthyAsync();
            var ready = await _client.IsReadyAsync();
            StatusMessage = IsHealthy
                ? (ready ? "✓ healthy & ready" : "⚠ healthy but not ready (coordinator may be starting up)")
                : "✗ coordinator unreachable";
        }
        catch (Exception ex)
        {
            _log.LogWarning(ex, "health check failed");
            IsHealthy = false;
            StatusMessage = $"✗ {ex.Message}";
        }
        finally
        {
            IsChecking = false;
        }
    }

    /// <summary>
    /// Calls <c>POST /admin/reset</c> on both default participants to
    /// re-seed their in-memory state (cash balances, share inventory)
    /// to the demo's initial values. Used by the Race Demo modal: after
    /// the first run drains the inventory, the user can click "Reset
    /// demo data" to make the demo re-runnable.
    /// </summary>
    [RelayCommand(CanExecute = nameof(CanReset))]
    public async Task ResetDemoDataAsync()
    {
        if (IsResetting) return;
        IsResetting = true;
        ResetMessage = null;
        try
        {
            var accounts = await _client.ResetParticipantAsync(AccountsParticipantUrl);
            var inventory = await _client.ResetParticipantAsync(InventoryParticipantUrl);
            if (accounts && inventory)
            {
                ResetMessage = "✓ demo data reset: A=10000, B=10000, AAPL=100, MSFT=500";
            }
            else
            {
                var failed = !accounts ? "accounts-svc" : "";
                if (!inventory) failed += (failed.Length > 0 ? ", " : "") + "inventory-svc";
                ResetMessage = $"✗ reset failed on: {failed}";
            }
        }
        catch (Exception ex)
        {
            _log.LogWarning(ex, "reset demo data failed");
            ResetMessage = $"✗ {ex.Message}";
        }
        finally
        {
            IsResetting = false;
        }
    }

    private bool CanReset() => !IsResetting;

    partial void OnIsResettingChanged(bool value) => ResetDemoDataCommand.NotifyCanExecuteChanged();
}
