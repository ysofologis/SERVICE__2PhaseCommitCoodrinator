using Avalonia.Controls;
using Avalonia.Markup.Xaml;
using TradingClient.ViewModels;

namespace TradingClient.Views;

public partial class MainWindow : Window
{
    public MainWindow()
    {
        InitializeComponent();
        // Kick the startup probe once the window is shown so the status
        // bar updates as soon as the UI is up. Using Loaded event keeps
        // us off the constructor, where the DataContext may not be set.
        Loaded += async (_, _) =>
        {
            if (DataContext is MainWindowViewModel vm)
                await vm.StartupProbeCommand.ExecuteAsync(null);
        };
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);
}
