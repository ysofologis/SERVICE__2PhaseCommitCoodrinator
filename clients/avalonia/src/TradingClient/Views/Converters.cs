using System;
using System.Collections.Generic;
using System.Globalization;
using Avalonia.Data.Converters;
using Avalonia.Media;
using TradingClient.Models;

namespace TradingClient.Views;

/// <summary>
/// Maps a <see cref="TransactionStatus"/> to a brush for the badge in
/// the orders list and the detail header.
/// </summary>
public sealed class StatusToBrushConverter : IValueConverter
{
    public static readonly StatusToBrushConverter Instance = new();

    public object? Convert(object? value, Type targetType, object? parameter, CultureInfo culture)
    {
        if (value is not TransactionStatus s) return Brushes.Gray;
        return s switch
        {
            TransactionStatus.Init or TransactionStatus.Preparing => new SolidColorBrush(Color.FromRgb(0x33, 0x66, 0xAA)),
            TransactionStatus.Committing => new SolidColorBrush(Color.FromRgb(0xAA, 0x88, 0x00)),
            TransactionStatus.Committed => new SolidColorBrush(Color.FromRgb(0x2E, 0x7D, 0x32)),
            TransactionStatus.Aborting or TransactionStatus.Aborted => new SolidColorBrush(Color.FromRgb(0xC6, 0x28, 0x28)),
            TransactionStatus.Failed => new SolidColorBrush(Color.FromRgb(0x6B, 0x1F, 0x1F)),
            _ => Brushes.Gray,
        };
    }

    public object? ConvertBack(object? value, Type targetType, object? parameter, CultureInfo culture)
        => throw new NotSupportedException();
}

public sealed class BoolToBrushConverter : IValueConverter
{
    public static readonly BoolToBrushConverter Instance = new();

    public object? Convert(object? value, Type targetType, object? parameter, CultureInfo culture)
        => value is true ? new SolidColorBrush(Color.FromRgb(0x2E, 0x7D, 0x32))
                         : new SolidColorBrush(Color.FromRgb(0xC6, 0x28, 0x28));

    public object? ConvertBack(object? value, Type targetType, object? parameter, CultureInfo culture)
        => throw new NotSupportedException();
}

public sealed class InverseBoolConverter : IValueConverter
{
    public static readonly InverseBoolConverter Instance = new();
    public object? Convert(object? value, Type targetType, object? parameter, CultureInfo culture) => value is bool b ? !b : value;
    public object? ConvertBack(object? value, Type targetType, object? parameter, CultureInfo culture) => value is bool b ? !b : value;
}

public sealed class NullToBoolConverter : IValueConverter
{
    public static readonly NullToBoolConverter Instance = new();
    public object? Convert(object? value, Type targetType, object? parameter, CultureInfo culture) => value is null;
    public object? ConvertBack(object? value, Type targetType, object? parameter, CultureInfo culture) => throw new NotSupportedException();
}

public sealed class NotNullToBoolConverter : IValueConverter
{
    public static readonly NotNullToBoolConverter Instance = new();
    public object? Convert(object? value, Type targetType, object? parameter, CultureInfo culture) => value is not null;
    public object? ConvertBack(object? value, Type targetType, object? parameter, CultureInfo culture) => throw new NotSupportedException();
}

/// <summary>
/// Converts a <see cref="Transaction"/> into a list of
/// <see cref="TimelineNode"/>s for the timeline ItemsControl. The nodes
/// show the timestamps recorded by the coordinator.
/// </summary>
public sealed class TimelineConverter : IValueConverter
{
    public static readonly TimelineConverter Instance = new();

    public object? Convert(object? value, Type targetType, object? parameter, CultureInfo culture)
    {
        if (value is not Transaction tx) return Array.Empty<TimelineNode>();
        var nodes = new List<TimelineNode>();
        if (tx.CreatedAt is { } created) nodes.Add(new("created", created));
        if (tx.UpdatedAt is { } updated && updated != tx.CreatedAt) nodes.Add(new("updated", updated));
        if (tx.DecisionAt is { } decision) nodes.Add(new(tx.StatusRaw?.ToLowerInvariant() ?? "decision", decision));
        if (tx.Participants != null)
        {
            foreach (var p in tx.Participants)
            {
                if (p.PreparedAt is { } pa) nodes.Add(new($"{p.Name} prepared", pa));
                if (p.CommittedAt is { } ca) nodes.Add(new($"{p.Name} committed", ca));
                if (p.AbortedAt is { } aa) nodes.Add(new($"{p.Name} aborted", aa));
            }
        }
        nodes.Sort((a, b) => a.At.CompareTo(b.At));
        return nodes;
    }

    public object? ConvertBack(object? value, Type targetType, object? parameter, CultureInfo culture)
        => throw new NotSupportedException();
}

public sealed record TimelineNode(string Label, DateTimeOffset At);
