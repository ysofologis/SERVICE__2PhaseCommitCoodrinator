using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace TradingClient.Models;

/// <summary>
/// Wire model for <c>GET /transactions/{id}</c> and the
/// <c>ListTransactionsResponse</c> entries in the Go coordinator.
/// Field names match the JSON exactly (snake_case) — the typed
/// HttpClient is configured with <see cref="System.Text.Json.JsonNamingPolicy.SnakeCaseLower"/>.
/// </summary>
public sealed record Transaction(
    [property: JsonPropertyName("id")] string Id,
    [property: JsonPropertyName("topic_key")] string? TopicKey,
    [property: JsonPropertyName("status")] string? StatusRaw,
    [property: JsonPropertyName("created_at")] DateTimeOffset? CreatedAt,
    [property: JsonPropertyName("updated_at")] DateTimeOffset? UpdatedAt,
    [property: JsonPropertyName("decision_at")] DateTimeOffset? DecisionAt,
    [property: JsonPropertyName("decided_by")] string? DecidedBy,
    [property: JsonPropertyName("error_message")] string? ErrorMessage,
    [property: JsonPropertyName("phases")] Phases? Phases,
    [property: JsonPropertyName("participants")] IReadOnlyList<Participant>? Participants
)
{
    [JsonIgnore]
    public TransactionStatus Status => TransactionStatusExtensions.Parse(StatusRaw);
}

public sealed record Phases(
    [property: JsonPropertyName("prepare")] string? Prepare,
    [property: JsonPropertyName("commit")] string? Commit
);

public sealed record Participant(
    [property: JsonPropertyName("name")] string Name,
    [property: JsonPropertyName("vote")] string? Vote,
    [property: JsonPropertyName("final")] string? Final,
    [property: JsonPropertyName("reason")] string? Reason,
    [property: JsonPropertyName("prepared_at")] DateTimeOffset? PreparedAt,
    [property: JsonPropertyName("committed_at")] DateTimeOffset? CommittedAt,
    [property: JsonPropertyName("aborted_at")] DateTimeOffset? AbortedAt
);
