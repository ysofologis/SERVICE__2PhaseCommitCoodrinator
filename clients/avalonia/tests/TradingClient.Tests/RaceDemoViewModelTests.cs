using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.Extensions.Logging.Abstractions;
using TradingClient.Models;
using TradingClient.Services;
using TradingClient.ViewModels;
using Xunit;

namespace TradingClient.Tests;

public class RaceDemoViewModelTests
{
    [Fact]
    public async Task RunAsync_marks_one_committed_and_one_aborted()
    {
        // Simulate the coordinator's correct behavior: the first sync
        // request commits (200 + COMMITTED), the second aborts because
        // the inventory is now empty (422 + ABORTED).
        var committed = JsonSerializer.Serialize(new
        {
            id = "tx_race_A-1",
            status = "COMMITTED",
            topic_key = "race-A",
        });
        var aborted = JsonSerializer.Serialize(new
        {
            id = "tx_race_B-1",
            status = "ABORTED",
            topic_key = "race-B",
            error_message = "out of stock",
        });
        int call = 0;
        var handler = new StubHandler(_ =>
        {
            call++;
            // First call returns committed; second returns aborted.
            return new HttpResponseMessage(call == 1 ? HttpStatusCode.OK : HttpStatusCode.UnprocessableEntity)
            {
                Content = new StringContent(call == 1 ? committed : aborted, Encoding.UTF8, "application/json"),
            };
        });
        var http = new HttpClient(handler) { BaseAddress = new Uri("http://test") };
        var client = new CoordinatorClient(http, NullLogger<CoordinatorClient>.Instance);
        var vm = new RaceDemoViewModel(client, NullLogger<RaceDemoViewModel>.Instance);

        await vm.RunCommand.ExecuteAsync(null);

        Assert.Contains("COMMITTED", vm.ResultA);
        Assert.Contains("ABORTED", vm.ResultB);
        Assert.Contains("Race prevented", vm.WinnerLabel);
    }

    [Fact]
    public async Task RunAsync_detects_over_sell_bug()
    {
        // If both transactions report COMMITTED, the race demo should
        // report OVER-SELL — the demo is the regression test.
        var ok = JsonSerializer.Serialize(new { id = "tx_A", status = "COMMITTED", topic_key = "x" });
        var handler = new StubHandler(_ => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(ok, Encoding.UTF8, "application/json"),
        });
        var http = new HttpClient(handler) { BaseAddress = new Uri("http://test") };
        var client = new CoordinatorClient(http, NullLogger<CoordinatorClient>.Instance);
        var vm = new RaceDemoViewModel(client, NullLogger<RaceDemoViewModel>.Instance);

        await vm.RunCommand.ExecuteAsync(null);

        Assert.Contains("OVER-SELL", vm.WinnerLabel);
    }

    private sealed class StubHandler : HttpMessageHandler
    {
        private readonly Func<HttpRequestMessage, HttpResponseMessage> _handler;
        public StubHandler(Func<HttpRequestMessage, HttpResponseMessage> handler) => _handler = handler;
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage req, CancellationToken ct)
            => Task.FromResult(_handler(req));
    }
}
