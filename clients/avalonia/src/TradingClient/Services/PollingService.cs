using System;
using System.Collections.Generic;
using System.Reactive.Linq;
using System.Reactive.Subjects;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using TradingClient.Models;

namespace TradingClient.Services;

/// <summary>
/// Background poller that fans out two streams:
/// - <see cref="ListStream"/>: latest list of recent transactions, every
///   <see cref="ListInterval"/>.
/// - <see cref="DetailStream"/>: latest snapshot of every transaction
///   whose id is currently in the watch set, every <see cref="DetailInterval"/>.
///
/// ViewModels subscribe to these streams and marshal updates back to the
/// UI thread via the Avalonia dispatcher. The watch set is mutated from
/// the UI thread (when the user selects an order) and read from the
/// polling loop (a background task); the lock keeps that race clean.
/// </summary>
public sealed class PollingService : BackgroundService
{
    private readonly ICoordinatorClient _client;
    private readonly ILogger<PollingService> _log;
    private readonly Subject<IReadOnlyList<Transaction>> _list = new();
    private readonly Subject<Transaction> _detail = new();
    private readonly HashSet<string> _watched = new();
    private readonly object _watchLock = new();

    public TimeSpan ListInterval { get; set; } = TimeSpan.FromSeconds(2);
    public TimeSpan DetailInterval { get; set; } = TimeSpan.FromSeconds(1);

    public IObservable<IReadOnlyList<Transaction>> ListStream => _list.AsObservable();
    public IObservable<Transaction> DetailStream => _detail.AsObservable();

    public void Watch(string id)
    {
        lock (_watchLock) _watched.Add(id);
    }

    public void Unwatch(string id)
    {
        lock (_watchLock) _watched.Remove(id);
    }

    public PollingService(ICoordinatorClient client, ILogger<PollingService> log)
    {
        _client = client;
        _log = log;
    }

    protected override async Task ExecuteAsync(CancellationToken ct)
    {
        // Wait briefly at startup so the coordinator has a chance to be
        // reachable before the first poll. The first tick still runs
        // immediately after, so the UI populates fast.
        try { await Task.Delay(TimeSpan.FromMilliseconds(250), ct); }
        catch (OperationCanceledException) { return; }

        var listTick = 0;
        while (!ct.IsCancellationRequested)
        {
            try
            {
                // 1. List tick
                var txs = await _client.ListTransactionsAsync(limit: 25, ct: ct);
                _list.OnNext(txs);
                listTick = 0;

                // 2. Detail ticks: one HTTP call per watched id
                string[] watched;
                lock (_watchLock) watched = _watched.ToArray();
                foreach (var id in watched)
                {
                    if (ct.IsCancellationRequested) break;
                    var tx = await _client.GetTransactionAsync(id, ct);
                    if (tx != null) _detail.OnNext(tx);
                }
            }
            catch (OperationCanceledException) { break; }
            catch (Exception ex)
            {
                _log.LogWarning(ex, "poll cycle failed");
                listTick++;
                // Back off slightly on consecutive failures so we don't
                // hammer a coordinator that's coming back up.
                if (listTick > 2)
                {
                    try { await Task.Delay(TimeSpan.FromSeconds(2), ct); }
                    catch (OperationCanceledException) { break; }
                }
            }

            try { await Task.Delay(ListInterval, ct); }
            catch (OperationCanceledException) { break; }
        }
    }
}
