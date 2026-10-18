using CommunityToolkit.Mvvm.ComponentModel;

namespace TradingClient.ViewModels;

/// <summary>
/// Common base for all ViewModels. Inherits <see cref="ObservableObject"/>
/// so <c>[ObservableProperty]</c>-generated properties raise
/// <c>PropertyChanged</c> on the right thread (the UI thread, since the
/// ViewModels are constructed in <c>App.OnFrameworkInitializationCompleted</c>).
/// </summary>
public abstract partial class ViewModelBase : ObservableObject
{
}
