using Avalonia;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using System;

namespace TradingClient;

internal static class Program
{
    [STAThread]
    public static void Main(string[] args)
    {
        // Avalonia headless / desktop bootstrap. AppBuilder is configured
        // in App.axaml.cs. We host the DI container as a generic host so
        // the polling service and typed HttpClient get the same lifetime
        // management as the ViewModels.
        var host = Host.CreateDefaultBuilder(args)
            .ConfigureServices((ctx, services) =>
            {
                services.AddTradingClient(ctx.Configuration["Coordinator:Url"] ?? "http://localhost:8080");
            })
            .Build();

        App.Services = host.Services;

        BuildAvaloniaApp().StartWithClassicDesktopLifetime(args);
    }

    public static AppBuilder BuildAvaloniaApp()
        => AppBuilder.Configure<App>()
            .UsePlatformDetect()
            .WithInterFont()
            .LogToTrace();
}
