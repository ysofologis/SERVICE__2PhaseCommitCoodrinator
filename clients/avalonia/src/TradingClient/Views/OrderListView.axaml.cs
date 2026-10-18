using Avalonia.Controls;
using Avalonia.Markup.Xaml;

namespace TradingClient.Views;

public partial class OrderListView : UserControl
{
    public OrderListView() { InitializeComponent(); }
    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);
}
