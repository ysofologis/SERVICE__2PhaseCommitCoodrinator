using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Http;
using System.Net.Http.Json;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.Extensions.Logging;
using TradingClient.Models;

namespace TradingClient.Services;

/// <summary>
/// Typed <see cref="HttpClient"/> wrapper around the Go 2PC coordinator.
/// Registered via <c>AddHttpClient&lt;ICoordinatorClient, CoordinatorClient&gt;</c>
/// so the framework manages the handler lifetime and DNS refresh.
/// </summary>
public sealed class CoordinatorClient : ICoordinatorClient
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        PropertyNameCaseInsensitive = true,
    };

    private readonly HttpClient _http;
    private readonly ILogger<CoordinatorClient> _log;

    public CoordinatorClient(HttpClient http, ILogger<CoordinatorClient> log)
    {
        _http = http;
        _log = log;
    }

    public async Task<bool> IsHealthyAsync(CancellationToken ct = default)
    {
        try
        {
            var resp = await _http.GetAsync("/healthz", ct);
            return resp.StatusCode == HttpStatusCode.OK;
        }
        catch (Exception ex)
        {
            _log.LogDebug(ex, "healthz failed");
            return false;
        }
    }

    public async Task<bool> IsReadyAsync(CancellationToken ct = default)
    {
        try
        {
            var resp = await _http.GetAsync("/readyz", ct);
            return resp.StatusCode == HttpStatusCode.OK;
        }
        catch (Exception ex)
        {
            _log.LogDebug(ex, "readyz failed");
            return false;
        }
    }

    public async Task<IReadOnlyList<Transaction>> ListTransactionsAsync(int limit, CancellationToken ct = default)
    {
        try
        {
            // The Go coordinator returns either a JSON array or `null`
            // (when the log is empty). JsonSerializer handles `null` as
            // null; we map that to an empty list for the UI.
            var list = await _http.GetFromJsonAsync<List<Transaction>>(
                $"/transactions?limit={limit}", JsonOptions, ct);
            return list ?? new List<Transaction>();
        }
        catch (Exception ex)
        {
            _log.LogWarning(ex, "ListTransactions failed");
            return new List<Transaction>();
        }
    }

    public async Task<Transaction?> GetTransactionAsync(string id, CancellationToken ct = default)
    {
        try
        {
            var resp = await _http.GetAsync($"/transactions/{Uri.EscapeDataString(id)}", ct);
            if (resp.StatusCode == HttpStatusCode.NotFound) return null;
            if (!resp.IsSuccessStatusCode) return null;
            return await resp.Content.ReadFromJsonAsync<Transaction>(JsonOptions, ct);
        }
        catch (Exception ex)
        {
            _log.LogWarning(ex, "GetTransaction({Id}) failed", id);
            return null;
        }
    }

    public async Task<CreateTransactionResult> CreateTransactionAsync(
        CreateTransactionRequest request,
        bool sync = false,
        CancellationToken ct = default)
    {
        try
        {
            var url = sync ? "/transactions?sync=true" : "/transactions";
            var resp = await _http.PostAsJsonAsync(url, request, JsonOptions, ct);

            if (resp.StatusCode == HttpStatusCode.Accepted)
            {
                // Async path: body is { id, status, topic_key, message }
                var accepted = await resp.Content.ReadFromJsonAsync<AsyncAcceptedResponse>(JsonOptions, ct);
                if (accepted is null)
                    return new CreateTransactionResult(false, null, 202, "empty body");
                return new CreateTransactionResult(
                    true, accepted.Id, 202, null,
                    TransactionStatusExtensions.Parse(accepted.Status));
            }

            if (resp.StatusCode == HttpStatusCode.OK)
            {
                // Sync path: body is the full Transaction (terminal state)
                var tx = await resp.Content.ReadFromJsonAsync<Transaction>(JsonOptions, ct);
                if (tx is null)
                    return new CreateTransactionResult(false, null, 200, "empty body");
                return new CreateTransactionResult(true, tx.Id, 200, null, tx.Status);
            }

            // Error path: read the body once as a JsonDocument so we can
            // inspect the shape before deciding which type to deserialize
            // as. The Go coordinator returns one of:
            //   - {"error": "..."}                       (handler error)
            //   - full Transaction (sync mode at 422)   (vote-no abort)
            //   - full Transaction with status=ABORTED   (timeout abort)
            string? errMsg = null;
            TransactionStatus errorStatus = TransactionStatus.Unknown;
            try
            {
                var stream = await resp.Content.ReadAsStreamAsync(ct);
                using var doc = await System.Text.Json.JsonDocument.ParseAsync(stream, cancellationToken: ct);
                var root = doc.RootElement;
                if (root.ValueKind == System.Text.Json.JsonValueKind.Object
                    && root.TryGetProperty("error", out var errEl)
                    && errEl.ValueKind == System.Text.Json.JsonValueKind.String)
                {
                    errMsg = errEl.GetString();
                }
                else
                {
                    // Re-parse as a Transaction. (We re-read the stream
                    // here because JsonDocument consumes it.)
                    stream.Position = 0;
                    var tx = await System.Text.Json.JsonSerializer.DeserializeAsync<Transaction>(stream, JsonOptions, ct);
                    if (tx != null)
                    {
                        errMsg = tx.ErrorMessage;
                        errorStatus = tx.Status;
                    }
                }
            }
            catch { /* ignore */ }

            return new CreateTransactionResult(
                false, null, (int)resp.StatusCode,
                errMsg ?? $"HTTP {(int)resp.StatusCode}",
                errorStatus);
        }
        catch (Exception ex)
        {
            _log.LogWarning(ex, "CreateTransaction failed");
            return new CreateTransactionResult(false, null, 0, ex.Message);
        }
    }

    public async Task<bool> CancelTransactionAsync(string id, CancellationToken ct = default)
    {
        try
        {
            var resp = await _http.PostAsync(
                $"/transactions/{Uri.EscapeDataString(id)}/cancel",
                new StringContent(""), ct);
            return resp.StatusCode == HttpStatusCode.Accepted;
        }
        catch (Exception ex)
        {
            _log.LogWarning(ex, "CancelTransaction({Id}) failed", id);
            return false;
        }
    }

    public async Task<bool> ResetParticipantAsync(string participantBaseUrl, CancellationToken ct = default)
    {
        // The typed HttpClient's BaseAddress is bound to the coordinator.
        // For the participant we have to construct a one-off HttpClient
        // — this is rare (only called when the user clicks "Reset demo
        // data" in the Dev tools drawer), so we don't worry about
        // socket pooling here.
        try
        {
            using var http = new HttpClient { BaseAddress = new Uri(participantBaseUrl), Timeout = TimeSpan.FromSeconds(5) };
            var resp = await http.PostAsync("/admin/reset", new StringContent(""), ct);
            return resp.IsSuccessStatusCode;
        }
        catch (Exception ex)
        {
            _log.LogWarning(ex, "ResetParticipant({Url}) failed", participantBaseUrl);
            return false;
        }
    }
}
