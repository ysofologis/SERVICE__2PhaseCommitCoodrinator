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
using Xunit;

namespace TradingClient.Tests;

public class CoordinatorClientTests
{
    [Fact]
    public async Task IsHealthyAsync_returns_true_on_200()
    {
        var handler = new StubHandler(_ => HttpResponse(HttpStatusCode.OK, """{"status":"ok"}"""));
        var sut = MakeClient(handler);
        Assert.True(await sut.IsHealthyAsync());
    }

    [Fact]
    public async Task IsHealthyAsync_returns_false_on_500()
    {
        var handler = new StubHandler(_ => HttpResponse(HttpStatusCode.InternalServerError, "{}"));
        var sut = MakeClient(handler);
        Assert.False(await sut.IsHealthyAsync());
    }

    [Fact]
    public async Task IsReadyAsync_returns_true_on_200()
    {
        var handler = new StubHandler(_ => HttpResponse(HttpStatusCode.OK, """{"status":"ready"}"""));
        var sut = MakeClient(handler);
        Assert.True(await sut.IsReadyAsync());
    }

    [Fact]
    public async Task ListTransactionsAsync_handles_null_body()
    {
        // The Go server emits `null` when the log is empty.
        var handler = new StubHandler(_ => HttpResponse(HttpStatusCode.OK, "null"));
        var sut = MakeClient(handler);
        var result = await sut.ListTransactionsAsync(10);
        Assert.Empty(result);
    }

    [Fact]
    public async Task ListTransactionsAsync_parses_array()
    {
        var body = JsonSerializer.Serialize(new[]
        {
            new { id = "tx_1", topic_key = "tk", status = "COMMITTED" },
            new { id = "tx_2", topic_key = "tk2", status = "ABORTED" },
        });
        var handler = new StubHandler(_ => HttpResponse(HttpStatusCode.OK, body));
        var sut = MakeClient(handler);
        var result = await sut.ListTransactionsAsync(10);
        Assert.Equal(2, result.Count);
        Assert.Equal("tx_1", result[0].Id);
        Assert.Equal(TransactionStatus.Committed, result[0].Status);
    }

    [Fact]
    public async Task GetTransactionAsync_returns_null_on_404()
    {
        var handler = new StubHandler(_ => HttpResponse(HttpStatusCode.NotFound, """{"error":"not found"}"""));
        var sut = MakeClient(handler);
        Assert.Null(await sut.GetTransactionAsync("missing"));
    }

    [Fact]
    public async Task CreateTransactionAsync_parses_202_accepted()
    {
        var body = JsonSerializer.Serialize(new
        {
            id = "tx_async-1",
            status = "INIT",
            topic_key = "tk",
            message = "accepted",
        });
        var handler = new StubHandler(req =>
        {
            // The async path is /transactions; sync is /transactions?sync=true.
            Assert.Equal("/transactions", req.RequestUri!.AbsolutePath);
            return HttpResponse(HttpStatusCode.Accepted, body);
        });
        var sut = MakeClient(handler);
        var result = await sut.CreateTransactionAsync(
            new CreateTransactionRequest("tk", 10000, new List<Operation>()),
            sync: false);
        Assert.True(result.Success);
        Assert.Equal("tx_async-1", result.Id);
        Assert.Equal(202, result.StatusCode);
        Assert.Equal(TransactionStatus.Init, result.Status);
    }

    [Fact]
    public async Task CreateTransactionAsync_parses_200_sync()
    {
        var body = JsonSerializer.Serialize(new
        {
            id = "tx_sync-1",
            status = "COMMITTED",
            topic_key = "tk",
        });
        var handler = new StubHandler(req =>
        {
            Assert.Equal("/transactions", req.RequestUri!.AbsolutePath);
            Assert.Equal("true", System.Web.HttpUtility.ParseQueryString(req.RequestUri.Query)["sync"]);
            return HttpResponse(HttpStatusCode.OK, body);
        });
        var sut = MakeClient(handler);
        var result = await sut.CreateTransactionAsync(
            new CreateTransactionRequest("tk", 10000, new List<Operation>()),
            sync: true);
        Assert.True(result.Success);
        Assert.Equal(TransactionStatus.Committed, result.Status);
    }

    [Fact]
    public async Task CreateTransactionAsync_parses_422_error()
    {
        var body = """{"error":"participant voted no: insufficient funds"}""";
        var handler = new StubHandler(_ => HttpResponse(HttpStatusCode.UnprocessableEntity, body));
        var sut = MakeClient(handler);
        var result = await sut.CreateTransactionAsync(
            new CreateTransactionRequest("tk", 10000, new List<Operation>()));
        Assert.False(result.Success);
        Assert.Equal(422, result.StatusCode);
        Assert.Contains("insufficient funds", result.ErrorMessage);
    }

    [Fact]
    public async Task CancelTransactionAsync_returns_true_on_202()
    {
        var handler = new StubHandler(req =>
        {
            Assert.Equal("/transactions/tx_1/cancel", req.RequestUri!.AbsolutePath);
            return HttpResponse(HttpStatusCode.Accepted, """{"status":"cancelling"}""");
        });
        var sut = MakeClient(handler);
        Assert.True(await sut.CancelTransactionAsync("tx_1"));
    }

    [Fact]
    public async Task ResetParticipantAsync_returns_true_on_200()
    {
        // Spin up a real local HTTP listener that responds 200 to
        // /admin/reset. The SUT's ResetParticipantAsync creates its
        // own HttpClient and hits the given base URL; we can intercept
        // it by binding to a free loopback port.
        var listener = new System.Net.HttpListener();
        var port = GetFreeTcpPort();
        listener.Prefixes.Add($"http://127.0.0.1:{port}/");
        listener.Start();
        var serverTask = Task.Run(async () =>
        {
            try
            {
                while (listener.IsListening)
                {
                    var ctx = await listener.GetContextAsync();
                    Assert.Equal("POST", ctx.Request.HttpMethod);
                    Assert.Equal("/admin/reset", ctx.Request.Url!.AbsolutePath);
                    var bytes = System.Text.Encoding.UTF8.GetBytes("""{"status":"reset"}""");
                    ctx.Response.ContentType = "application/json";
                    ctx.Response.ContentLength64 = bytes.Length;
                    await ctx.Response.OutputStream.WriteAsync(bytes);
                    ctx.Response.Close();
                }
            }
            catch (System.Net.HttpListenerException) { /* listener stopped */ }
            catch (ObjectDisposedException) { /* listener stopped */ }
        });

        try
        {
            var sut = MakeClient(new StubHandler(_ => HttpResponse(HttpStatusCode.OK, "{}")));
            var result = await sut.ResetParticipantAsync($"http://127.0.0.1:{port}");
            Assert.True(result);
        }
        finally
        {
            listener.Stop();
            listener.Close();
            try { await serverTask; } catch { /* expected */ }
        }
    }

    [Fact]
    public async Task ResetParticipantAsync_returns_false_on_unreachable()
    {
        var sut = MakeClient(new StubHandler(_ => HttpResponse(HttpStatusCode.OK, "{}")));
        // Port 1 is reserved/unused; connecting should fail quickly.
        var result = await sut.ResetParticipantAsync("http://127.0.0.1:1");
        Assert.False(result);
    }

    private static int GetFreeTcpPort()
    {
        var l = new System.Net.Sockets.TcpListener(System.Net.IPAddress.Loopback, 0);
        l.Start();
        try { return ((System.Net.IPEndPoint)l.LocalEndpoint).Port; }
        finally { l.Stop(); }
    }

    // ---- helpers ---------------------------------------------------------

    private static CoordinatorClient MakeClient(StubHandler handler)
    {
        var http = new HttpClient(handler) { BaseAddress = new Uri("http://test") };
        return new CoordinatorClient(http, NullLogger<CoordinatorClient>.Instance);
    }

    private static HttpResponseMessage HttpResponse(HttpStatusCode code, string body)
        => new(code)
        {
            Content = new StringContent(body, Encoding.UTF8, "application/json"),
        };

    private sealed class StubHandler : HttpMessageHandler
    {
        private readonly Func<HttpRequestMessage, HttpResponseMessage> _handler;
        public StubHandler(Func<HttpRequestMessage, HttpResponseMessage> handler) => _handler = handler;

        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage req, CancellationToken ct)
            => Task.FromResult(_handler(req));
    }
}
