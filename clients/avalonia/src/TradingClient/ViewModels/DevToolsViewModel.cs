using System;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Microsoft.Extensions.Logging;
using TradingClient.Services;

namespace TradingClient.ViewModels;

/// <summary>
/// Backs the Dev tools drawer. Surfaces the coordinator URL, the
/// health status, and a button to call the (future) participant
/// <c>POST /admin/reset</c> endpoint to make the race demo re-runnable.
/// </summary>
public sealed partial class DevToolsViewModel : ViewModelBase
{
    private readonly ICoordinatorClient _client;
    private readonly ILogger<DevToolsViewModel> _log;

    [ObservableProperty] private string _coordinatorUrl = "http://localhost:8080";
    [ObservableProperty] private bool _isHealthy;
    [ObservableProperty] private string? _statusMessage;
    [ObservableProperty] private bool _isChecking;

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
}
