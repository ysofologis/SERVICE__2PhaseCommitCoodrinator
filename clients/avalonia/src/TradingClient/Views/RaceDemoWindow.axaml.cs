using Avalonia.Controls;
using Avalonia.Markup.Xaml;

namespace TradingClient.Views;

public partial class RaceDemoWindow : Window
{
    public RaceDemoWindow() { InitializeComponent(); }
    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);
}
