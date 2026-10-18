using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace TradingClient.Models;

/// <summary>
/// Request body for <c>POST /transactions</c>. Mirrors
/// <c>CreateTransactionRequest</c> in <c>internal/api/types.go</c>.
/// </summary>
public sealed record CreateTransactionRequest(
    [property: JsonPropertyName("topic_key")] string TopicKey,
    [property: JsonPropertyName("timeout_ms")] int TimeoutMs,
    [property: JsonPropertyName("operations")] IReadOnlyList<Operation> Operations
);

public sealed record Operation(
    [property: JsonPropertyName("participant")] string Participant,
    [property: JsonPropertyName("action")] string Action,
    [property: JsonPropertyName("params")] IReadOnlyDictionary<string, object>? Params
);

/// <summary>
/// Response wrapper for async <c>POST /transactions</c> (202 Accepted).
/// The sync path returns the full <see cref="Transaction"/> instead.
/// </summary>
public sealed record AsyncAcceptedResponse(
    [property: JsonPropertyName("id")] string Id,
    [property: JsonPropertyName("status")] string? Status,
    [property: JsonPropertyName("topic_key")] string? TopicKey,
    [property: JsonPropertyName("message")] string? Message
);

public sealed record ErrorResponse(
    [property: JsonPropertyName("error")] string? Error
);
