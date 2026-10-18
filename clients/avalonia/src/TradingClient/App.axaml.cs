using Avalonia;
using Avalonia.Controls.ApplicationLifetimes;
using Avalonia.Markup.Xaml;
using Microsoft.Extensions.DependencyInjection;
using TradingClient.ViewModels;
using TradingClient.Views;

namespace TradingClient;

public partial class App : Application
{
    public static IServiceProvider Services { get; set; } = null!;

    public override void Initialize()
    {
        AvaloniaXamlLoader.Load(this);
    }

    public override void OnFrameworkInitializationCompleted()
    {
        if (ApplicationLifetime is IClassicDesktopStyleApplicationLifetime desktop)
        {
            var main = Services.GetRequiredService<MainWindow>();
            desktop.MainWindow = main;
        }
        base.OnFrameworkInitializationCompleted();
    }
}
