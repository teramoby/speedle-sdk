// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Linq;
using System.Net.Http;
using System.Security.Authentication;
using System.Threading;
using System.Threading.Tasks;
using Xunit;

namespace Speedle.Sdk.Tests;

public class ClientTests
{
    private const string Allow = "{\"allowed\":true,\"reason\":0}";

    private readonly TestClock clock = new();
    private readonly Recorder recorder = new();
    private readonly FakeHandler handler = new();

    private SpeedleClient Client(
        Backend backend = Backend.SpeedleCloud,
        RetryPolicy? retry = null,
        FailureMode mode = FailureMode.Error,
        CircuitBreakerPolicy? breaker = null,
        SpeedleHooks? hooks = null,
        IClock? withClock = null) =>
        new(
            new SpeedleClientOptions
            {
                Backend = backend,
                Endpoint = new Uri("https://ads.example/prefix/"),
                Auth = backend == Backend.SpeedleCloud ? Auth.Bearer("spdl_t") : Auth.None(),
                ServiceName = "books",
                Retry = retry ?? new RetryPolicy(),
                FailureMode = mode,
                CircuitBreaker = breaker,
                Hooks = hooks ?? recorder.Hooks,
                UserAgentSuffix = "app/1",
            },
            withClock ?? clock,
            () => 1.0,
            handler);

    [Fact]
    public async Task SendsTheRequest()
    {
        handler.Reply(200, Allow, ("X-Quota-Daily-Remaining", "41"));
        using SpeedleClient c = Client();
        Decision d = await c.CheckAsync("alice", "get", "/r");
        Assert.True(d.Allowed);
        Assert.Equal(41L, d.QuotaDailyRemaining);
        SentRequest r = Assert.Single(handler.Requests);
        Assert.Equal("POST", r.Method);
        Assert.Equal("https://ads.example/prefix/authz-check/v1/is-allowed", r.Uri.ToString());
        Assert.Equal("Bearer spdl_t", r.Headers["Authorization"]);
        Assert.Equal("application/json", r.Headers["Content-Type"]);
        Assert.StartsWith("speedle-sdk-dotnet/", r.Headers["User-Agent"], StringComparison.Ordinal);
        Assert.EndsWith(" app/1", r.Headers["User-Agent"], StringComparison.Ordinal);
    }

    [Fact]
    public async Task SpeedlePlusSendsNoAuthorization()
    {
        handler.Reply(200, Allow);
        using SpeedleClient c = Client(Backend.SpeedlePlus);
        Assert.True(await c.IsAllowedAsync("alice", "get", "/r"));
        Assert.DoesNotContain("Authorization", Assert.Single(handler.Requests).Headers.Keys);
    }

    [Theory]
    [InlineData(404, "{\"error\":\"nf\"}", typeof(HttpException), 1)]
    [InlineData(302, "", typeof(HttpException), 1)]
    [InlineData(201, Allow, typeof(HttpException), 1)]
    [InlineData(413, "{\"error\":\"request_too_large\"}", typeof(RequestTooLargeException), 1)]
    [InlineData(415, "", typeof(HttpException), 1)]
    [InlineData(500, "{\"error\":\"internal_error\"}", typeof(ServerException), 1)]
    [InlineData(501, "", typeof(ServerException), 1)]
    [InlineData(502, "<html>", typeof(ServerException), 3)]
    [InlineData(504, "", typeof(ServerException), 3)]
    [InlineData(503, "", typeof(ServiceUnavailableException), 3)]
    [InlineData(429, "{\"error\":\"other\"}", typeof(TooManyRequestsException), 1)]
    [InlineData(429, "nope", typeof(TooManyRequestsException), 1)]
    [InlineData(200, "{\"allowed\":\"true\",\"reason\":0}", typeof(UnexpectedResponseException), 1)]
    [InlineData(200, "not json", typeof(UnexpectedResponseException), 1)]
    public async Task StatusMapping(int status, string body, Type type, int requests)
    {
        handler.Reply(status, body).Reply(status, body).Reply(status, body);
        using SpeedleClient c = Client();
        SpeedleException e = await Assert.ThrowsAnyAsync<SpeedleException>(() => c.IsAllowedAsync("alice", "get", "/r"));
        Assert.Equal(type, e.GetType());
        Assert.Equal(status, e.Status);
        Assert.Equal(requests, handler.Requests.Count);
        Assert.DoesNotContain("spdl_t", e.Message, StringComparison.Ordinal);
    }

    [Fact]
    public async Task SpeedlePlusErrorsAreMessagesAndGateway429IsBurst()
    {
        handler.Reply(400, "{\"error\":\"invalid_request\"}").Reply(429, "slow down");
        using SpeedleClient c = Client(Backend.SpeedlePlus);
        InvalidRequestException e = await Assert.ThrowsAsync<InvalidRequestException>(() => c.IsAllowedAsync("a", "g", "/r"));
        Assert.Null(e.Code);
        Assert.Contains("invalid_request", e.Message, StringComparison.Ordinal);
        RateLimitedException r = await Assert.ThrowsAsync<RateLimitedException>(() => c.IsAllowedAsync("a", "g", "/r"));
        Assert.Null(r.RetryAfter);
        Assert.Equal(2, handler.Requests.Count);
        Assert.Equal("non_retryable", recorder.Events[^1]["reason"]);
    }

    [Fact]
    public async Task BurstRetryAfterIsHonoured()
    {
        handler.Reply(429, "{\"error\":\"rate_limited\"}", ("Retry-After", "1")).Reply(200, Allow);
        using SpeedleClient c = Client();
        Assert.True(await c.IsAllowedAsync("a", "g", "/r"));
        Assert.Equal(new List<long> { 1000 }, clock.TakeSleepsMs());
    }

    [Fact]
    public async Task UnknownReasonAndErrorMessage()
    {
        handler.Reply(200, "{\"allowed\":false,\"reason\":4,\"errorMessage\":\"bad\"}").Reply(200, "{\"allowed\":true,\"reason\":99}");
        using SpeedleClient c = Client(Backend.SpeedlePlus);
        Decision d = await c.CheckAsync("a", "g", "/r");
        Assert.Equal(Reason.ErrorInEvaluation, d.Reason);
        Assert.Equal("bad", d.ErrorMessage);
        Decision u = await c.CheckAsync("a", "g", "/r", new Dictionary<string, object?> { ["x"] = 1 });
        Assert.Equal(99, (int)u.Reason);
    }

    [Fact]
    public async Task PerCallServiceName()
    {
        handler.Reply(200, Allow);
        using SpeedleClient c = Client();
        await c.IsAllowedAsync("a", "g", "/r", options: new CallOptions { ServiceName = "orders" });
        Assert.Contains("\"serviceName\":\"orders\"", Assert.Single(handler.Requests).Body, StringComparison.Ordinal);
        await Assert.ThrowsAsync<InvalidRequestException>(
            () => c.IsAllowedAsync("a", "g", "/r", options: new CallOptions { ServiceName = "" }));
    }

    [Fact]
    public async Task FailClosedNeverReturnsTrueAndCheckStillThrows()
    {
        handler.Reply(401, "{\"error\":\"unauthorized\"}").Reply(500, "");
        using SpeedleClient c = Client(mode: FailureMode.Closed);
        Assert.False(await c.IsAllowedAsync("a", "g", "/r"));
        await Assert.ThrowsAsync<ServerException>(() => c.CheckAsync("a", "g", "/r"));
        Assert.False(await c.IsAllowedAsync("a", "g", "/r", new Dictionary<string, object?> { ["x"] = null }));
        Assert.Equal(2, handler.Requests.Count);
        Dictionary<string, object?> last = recorder.Events[^1];
        Assert.Equal("giveUp", last["type"]);
        Assert.Equal(0, last["attempts"]);
        Assert.Equal("InvalidRequestError", last["errorType"]);
    }

    [Fact]
    public async Task ThrowingHooksAreIgnored()
    {
        handler.Reply(503, "").Reply(200, Allow);
        var bad = new SpeedleHooks
        {
            OnAttempt = _ => throw new InvalidOperationException("hook"),
            OnRetry = _ => throw new InvalidOperationException("hook"),
        };
        using SpeedleClient c = Client(Backend.SpeedlePlus, hooks: bad);
        Assert.True(await c.IsAllowedAsync("a", "g", "/r"));
    }

    [Fact]
    public async Task NetworkErrorsAreRetriedButTlsFailuresAreNot()
    {
        handler.Throw(new HttpRequestException(HttpRequestError.ConnectionError, "reset"))
            .Reply(200, Allow)
            .Throw(new HttpRequestException("tls", new AuthenticationException("bad cert")));
        using SpeedleClient c = Client(Backend.SpeedlePlus);
        Assert.True(await c.IsAllowedAsync("a", "g", "/r"));
        NetworkException e = await Assert.ThrowsAsync<NetworkException>(() => c.IsAllowedAsync("a", "g", "/r"));
        Assert.Null(e.Status);
        Assert.Equal(3, handler.Requests.Count);
    }

    [Fact]
    public async Task AttemptTimeoutIsRetriedAndDistinctFromDeadline()
    {
        handler.Hang().Hang();
        var retry = new RetryPolicy { MaxAttempts = 2, BaseDelay = TimeSpan.Zero, AttemptTimeout = TimeSpan.FromMilliseconds(100) };
        using SpeedleClient c = Client(Backend.SpeedlePlus, retry);
        SpeedleTimeoutException e = await Assert.ThrowsAsync<SpeedleTimeoutException>(() => c.IsAllowedAsync("a", "g", "/r"));
        Assert.Equal(typeof(SpeedleTimeoutException), e.GetType());
        Assert.Equal(2, handler.Requests.Count);
        Assert.Equal("attempts_exhausted", recorder.Events[^1]["reason"]);
    }

    [Fact]
    public async Task DeadlineCutsTheAttempt()
    {
        handler.Hang();
        var retry = new RetryPolicy { Deadline = TimeSpan.FromMilliseconds(150) };
        using SpeedleClient c = Client(Backend.SpeedlePlus, retry);
        await Assert.ThrowsAsync<DeadlineExceededException>(() => c.IsAllowedAsync("a", "g", "/r"));
        Assert.Equal("deadline", recorder.Events[^1]["reason"]);
    }

    [Fact]
    public async Task CancellationDuringAnAttemptIsNeverConverted()
    {
        handler.Hang();
        using SpeedleClient c = Client(Backend.SpeedlePlus, mode: FailureMode.Closed, breaker: new CircuitBreakerPolicy { FailureThreshold = 1 });
        using var cts = new CancellationTokenSource(TimeSpan.FromMilliseconds(100));
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => c.IsAllowedAsync("a", "g", "/r", cancellationToken: cts.Token));
        Assert.DoesNotContain(recorder.Events, e => (string?)e["type"] == "giveUp");
        Assert.DoesNotContain(recorder.Events, e => (string?)e["type"] == "breaker");
    }

    [Fact]
    public async Task CancellationDuringTheRetryDelay()
    {
        handler.Reply(503, "");
        var retry = new RetryPolicy { BaseDelay = TimeSpan.FromSeconds(3), MaxDelay = TimeSpan.FromSeconds(3), Deadline = TimeSpan.FromSeconds(10) };
        using SpeedleClient c = Client(Backend.SpeedlePlus, retry, withClock: SystemClock.Instance);
        using var cts = new CancellationTokenSource(TimeSpan.FromMilliseconds(200));
        var watch = Stopwatch.StartNew();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => c.IsAllowedAsync("a", "g", "/r", cancellationToken: cts.Token));
        Assert.True(watch.Elapsed < TimeSpan.FromSeconds(2), "the delay was cut short");
    }

    [Fact]
    public async Task BreakerFailsFastAndValidationDoesNotCount()
    {
        handler.Reply(503, "").Reply(200, Allow);
        using SpeedleClient c = Client(
            Backend.SpeedlePlus, RetryPolicy.Disabled, breaker: new CircuitBreakerPolicy { FailureThreshold = 1, CoolDown = TimeSpan.FromSeconds(30) });
        await Assert.ThrowsAsync<ServiceUnavailableException>(() => c.IsAllowedAsync("a", "g", "/r"));
        await Assert.ThrowsAsync<CircuitOpenException>(() => c.IsAllowedAsync("a", "g", "/r"));
        clock.Advance(TimeSpan.FromSeconds(30));
        await Assert.ThrowsAsync<InvalidRequestException>(
            () => c.IsAllowedAsync("a", "g", "/r", new Dictionary<string, object?> { ["x"] = null }));
        Assert.True(await c.IsAllowedAsync("a", "g", "/r"));
        Assert.Equal(
            new[] { "closed>open", "open>half_open", "half_open>closed" },
            recorder.Events.Where(e => (string?)e["type"] == "breaker").Select(e => $"{e["from"]}>{e["to"]}"));
    }

    [Fact]
    public async Task DisposedClientRejectsCalls()
    {
        SpeedleClient c = Client();
        await c.DisposeAsync();
        c.Dispose();
        await Assert.ThrowsAsync<ObjectDisposedException>(() => c.IsAllowedAsync("a", "g", "/r"));
    }
}
