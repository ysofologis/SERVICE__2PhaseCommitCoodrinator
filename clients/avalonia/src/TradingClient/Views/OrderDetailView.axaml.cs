using Avalonia.Controls;
using Avalonia.Markup.Xaml;

namespace TradingClient.Views;

public partial class OrderDetailView : UserControl
{
    public OrderDetailView() { InitializeComponent(); }
    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);
}
