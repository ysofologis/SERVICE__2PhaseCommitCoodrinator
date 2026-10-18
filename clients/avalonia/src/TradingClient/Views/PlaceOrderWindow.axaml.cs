using Avalonia.Controls;
using Avalonia.Markup.Xaml;

namespace TradingClient.Views;

public partial class PlaceOrderWindow : Window
{
    public PlaceOrderWindow() { InitializeComponent(); }
    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);
}
