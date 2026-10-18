using System;
using System.Text.Json;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using TradingClient.Services;
using TradingClient.ViewModels;
using TradingClient.Views;

namespace TradingClient;

public static class ServiceCollectionExtensions
{
    /// <summary>
    /// Wire up the Avalonia app's DI graph: typed HttpClient, polling
    /// background service, ViewModels, and the main window.
    /// </summary>
    public static IServiceCollection AddTradingClient(
        this IServiceCollection services,
        string coordinatorUrl)
    {
        // Typed HttpClient — the framework owns the handler lifetime,
        // which avoids socket exhaustion if the user re-enters settings.
        services.AddHttpClient<ICoordinatorClient, CoordinatorClient>(c =>
        {
            c.BaseAddress = new Uri(coordinatorUrl);
            c.Timeout = TimeSpan.FromSeconds(15);
            c.DefaultRequestHeaders.Add("Accept", "application/json");
        });

        // Long-running background poller. Stops with the host.
        services.AddSingleton<PollingService>();
        services.AddHostedService(sp => sp.GetRequiredService<PollingService>());

        // ViewModels are singletons so the polling subscription lives
        // for the whole app. They are stateless w.r.t. the UI tree; the
        // Avalonia data-context binding handles re-rendering.
        services.AddSingleton<MainWindowViewModel>();
        services.AddSingleton<OrderListViewModel>();
        services.AddSingleton<OrderDetailViewModel>();
        services.AddSingleton<PlaceOrderViewModel>();
        services.AddSingleton<RaceDemoViewModel>();
        services.AddSingleton<DevToolsViewModel>();

        // The main window pulls its ViewModel from DI.
        services.AddSingleton<MainWindow>();

        return services;
    }
}
