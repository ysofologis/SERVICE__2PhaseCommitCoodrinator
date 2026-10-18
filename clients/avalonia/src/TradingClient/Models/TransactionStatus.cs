using System.Text.Json.Serialization;

namespace TradingClient.Models;

/// <summary>
/// Mirrors the Go coordinator's state names in <c>internal/coord/state.go</c>.
/// Values are kept as strings (rather than enums) so the C# code can
/// deserialize any future state the coordinator adds without code changes.
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter))]
public enum TransactionStatus
{
    Unknown,
    Init,
    Preparing,
    Committing,
    Committed,
    Aborting,
    Aborted,
    Failed,
}

public static class TransactionStatusExtensions
{
    public static bool IsTerminal(this TransactionStatus status)
        => status is TransactionStatus.Committed
            or TransactionStatus.Aborted
            or TransactionStatus.Failed;

    public static string DisplayName(this TransactionStatus status) => status switch
    {
        TransactionStatus.Init => "INIT",
        TransactionStatus.Preparing => "PREPARING",
        TransactionStatus.Committing => "COMMITTING",
        TransactionStatus.Committed => "COMMITTED",
        TransactionStatus.Aborting => "ABORTING",
        TransactionStatus.Aborted => "ABORTED",
        TransactionStatus.Failed => "FAILED",
        _ => "UNKNOWN",
    };

    public static TransactionStatus Parse(string? raw)
    {
        if (string.IsNullOrWhiteSpace(raw)) return TransactionStatus.Unknown;
        return raw.ToUpperInvariant() switch
        {
            "INIT" => TransactionStatus.Init,
            "PREPARING" => TransactionStatus.Preparing,
            "COMMITTING" => TransactionStatus.Committing,
            "COMMITTED" => TransactionStatus.Committed,
            "ABORTING" => TransactionStatus.Aborting,
            "ABORTED" => TransactionStatus.Aborted,
            "FAILED" => TransactionStatus.Failed,
            _ => TransactionStatus.Unknown,
        };
    }
}
