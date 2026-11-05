// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text;
using System.Threading;
using System.Threading.Tasks;

namespace Speedle.Sdk.Tests;

/// <summary>Virtual clock: Now is real monotonic time plus an offset; DelayAsync adds to it.</summary>
internal sealed class TestClock : IClock
{
    private readonly object gate = new();
    private readonly List<TimeSpan> sleeps = new();
    private TimeSpan offset;

    public TimeSpan Now
    {
        get
        {
            lock (gate)
            {
                return Stopwatch.GetElapsedTime(0) + offset;
            }
        }
    }

    public Task DelayAsync(TimeSpan delay, CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        lock (gate)
        {
            sleeps.Add(delay);
            offset += delay;
        }

        return Task.CompletedTask;
    }

    public void Advance(TimeSpan d)
    {
        lock (gate)
        {
            offset += d;
        }
    }

    public List<long> TakeSleepsMs()
    {
        lock (gate)
        {
            var r = sleeps.Select(s => (long)s.TotalMilliseconds).ToList();
            sleeps.Clear();
            return r;
        }
    }
}

/// <summary>A recorded request.</summary>
internal sealed record SentRequest(string Method, Uri Uri, Dictionary<string, string> Headers, string Body);

/// <summary>Answers scripted responses in order and records requests.</summary>
internal sealed class FakeHandler : HttpMessageHandler
{
    private readonly Queue<Func<CancellationToken, Task<HttpResponseMessage>>> script = new();

    public List<SentRequest> Requests { get; } = new();

    public FakeHandler Reply(int status, string? body = null, params (string Name, string Value)[] headers)
    {
        script.Enqueue(_ =>
        {
            var r = new HttpResponseMessage((HttpStatusCode)status);
            if (body is not null)
            {
                r.Content = new StringContent(body, Encoding.UTF8, "application/json");
            }

            foreach ((string n, string v) in headers)
            {
                r.Headers.TryAddWithoutValidation(n, v);
            }

            return Task.FromResult(r);
        });
        return this;
    }

    public FakeHandler Throw(Exception e)
    {
        script.Enqueue(_ => Task.FromException<HttpResponseMessage>(e));
        return this;
    }

    /// <summary>Never answers; completes only when the attempt is cancelled.</summary>
    public FakeHandler Hang()
    {
        script.Enqueue(async ct =>
        {
            await Task.Delay(Timeout.Infinite, ct).ConfigureAwait(false);
            throw new InvalidOperationException("unreachable");
        });
        return this;
    }

    protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
    {
        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        foreach (KeyValuePair<string, HeaderStringValues> h in request.Headers.NonValidated)
        {
            headers[h.Key] = h.Value.ToString();
        }

        string body = string.Empty;
        if (request.Content is not null)
        {
            foreach (KeyValuePair<string, IEnumerable<string>> h in request.Content.Headers)
            {
                headers[h.Key] = string.Join(",", h.Value);
            }

            body = await request.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
        }

        lock (Requests)
        {
            Requests.Add(new SentRequest(request.Method.Method, request.RequestUri!, headers, body));
        }

        Func<CancellationToken, Task<HttpResponseMessage>> next;
        lock (script)
        {
            next = script.Count > 0 ? script.Dequeue() : (_ => Task.FromResult(new HttpResponseMessage((HttpStatusCode)418)));
        }

        return await next(cancellationToken).ConfigureAwait(false);
    }
}

/// <summary>Records hook events in the conformance vector shape.</summary>
internal sealed class Recorder
{
    public List<Dictionary<string, object?>> Events { get; } = new();

    public SpeedleHooks Hooks => new()
    {
        OnAttempt = e => Add("attempt", ("attempt", e.Attempt), ("status", e.Status), ("errorType", Names.Error(e.ErrorType))),
        OnRetry = e => Add("retry", ("attempt", e.Attempt), ("delayMs", (long)e.Delay.TotalMilliseconds), ("errorType", Names.Error(e.ErrorType))),
        OnGiveUp = e => Add("giveUp", ("attempts", e.Attempts), ("reason", Names.Reason(e.Reason)), ("errorType", Names.Error(e.Error.GetType().Name))),
        OnBreakerStateChange = e => Add("breaker", ("from", Names.State(e.From)), ("to", Names.State(e.To))),
    };

    private void Add(string type, params (string Key, object? Value)[] fields)
    {
        var m = new Dictionary<string, object?> { ["type"] = type };
        foreach ((string k, object? v) in fields)
        {
            m[k] = v;
        }

        Events.Add(m);
    }
}

/// <summary>Maps .NET names to the §7 and vector names.</summary>
internal static class Names
{
    public static string? Error(string? dotnetName)
    {
        if (dotnetName is null)
        {
            return null;
        }

        string n = dotnetName.EndsWith("Exception", StringComparison.Ordinal) ? dotnetName[..^"Exception".Length] : dotnetName;
        return (n == "SpeedleTimeout" ? "Timeout" : n) + "Error";
    }

    public static string Reason(GiveUpReason r) => r switch
    {
        GiveUpReason.NonRetryable => "non_retryable",
        GiveUpReason.AttemptsExhausted => "attempts_exhausted",
        GiveUpReason.Deadline => "deadline",
        GiveUpReason.RetryAfterExceedsDeadline => "retry_after_exceeds_deadline",
        _ => "circuit_open",
    };

    public static string State(BreakerState s) => s switch
    {
        BreakerState.Closed => "closed",
        BreakerState.Open => "open",
        _ => "half_open",
    };
}
