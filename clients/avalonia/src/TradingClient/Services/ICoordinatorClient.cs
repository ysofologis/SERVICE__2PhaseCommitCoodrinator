using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using TradingClient.Models;

namespace TradingClient.Services;

public interface ICoordinatorClient
{
    /// <summary>
    /// True iff <c>GET /healthz</c> returns 200. Cheap liveness probe.
    /// </summary>
    Task<bool> IsHealthyAsync(CancellationToken ct = default);

    /// <summary>
    /// True iff <c>GET /readyz</c> returns 200. Used at startup to gate
    /// the "Place Order" button.
    /// </summary>
    Task<bool> IsReadyAsync(CancellationToken ct = default);

    /// <summary>
    /// Calls <c>GET /transactions?limit=N</c>. Returns an empty list on
    /// 200 with body <c>null</c> (which the Go server emits when the log
    /// is empty).
    /// </summary>
    Task<IReadOnlyList<Transaction>> ListTransactionsAsync(int limit, CancellationToken ct = default);

    /// <summary>
    /// Calls <c>GET /transactions/{id}</c>. Returns null on 404.
    /// </summary>
    Task<Transaction?> GetTransactionAsync(string id, CancellationToken ct = default);

    /// <summary>
    /// Calls <c>POST /transactions</c>. When <paramref name="sync"/> is
    /// true, sets <c>?sync=true</c> so the server blocks until terminal.
    /// On 200/202 returns the <c>transaction_id</c> and the observed
    /// status. On 4xx, returns the error message.
    /// </summary>
    Task<CreateTransactionResult> CreateTransactionAsync(
        CreateTransactionRequest request,
        bool sync = false,
        CancellationToken ct = default);

    /// <summary>
    /// Calls <c>POST /transactions/{id}/cancel</c>. Returns true on 202.
    /// </summary>
    Task<bool> CancelTransactionAsync(string id, CancellationToken ct = default);
}

public sealed record CreateTransactionResult(
    bool Success,
    string? Id,
    int StatusCode,
    string? ErrorMessage,
    TransactionStatus Status = TransactionStatus.Unknown);
