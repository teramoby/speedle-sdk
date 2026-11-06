// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Collections.Generic;
using System.Net.Http;
using System.Threading;
using System.Threading.Tasks;

namespace Speedle.Sdk;

/// <summary>
/// A client for <c>POST /authz-check/v1/is-allowed</c> on Speedle Cloud or self-hosted Speedle.
/// Create one per endpoint and share it: it is thread-safe and owns one keep-alive connection
/// pool. A cancelled <see cref="CancellationToken"/> stops the call, including during a retry
/// delay, and throws <see cref="OperationCanceledException"/> in every failure mode.
/// </summary>
public sealed class SpeedleClient : IDisposable, IAsyncDisposable
{
    private readonly ClientConfig config;
    private readonly Transport transport;
    private readonly CircuitBreaker? breaker;
    private int disposed;

    /// <summary>Validates the options and creates the client.</summary>
    /// <param name="options">The client settings.</param>
    /// <exception cref="ConfigurationException">The options are invalid.</exception>
    public SpeedleClient(SpeedleClientOptions options)
        : this(options, SystemClock.Instance, () => Random.Shared.NextDouble(), null)
    {
    }

    internal SpeedleClient(SpeedleClientOptions options, IClock clock, Func<double> random, HttpMessageHandler? handler)
    {
        config = ClientConfig.Validate(options, clock, random);
        transport = new Transport(
            config.Target, config.Backend, config.Auth, options.ExtraTrustedCertificates, config.UserAgent, handler);
        breaker = config.CircuitBreaker is null ? null : new CircuitBreaker(config.CircuitBreaker);
    }

    /// <summary>Whether the subject may perform the action on the resource.</summary>
    /// <param name="subject">The subject; a string is one <c>user</c> principal.</param>
    /// <param name="action">The action.</param>
    /// <param name="resource">The resource.</param>
    /// <param name="attributes">Typed attributes, sent in this order; null means none.</param>
    /// <param name="options">Per-call overrides.</param>
    /// <param name="cancellationToken">Cancels the call.</param>
    /// <returns>The server's <c>allowed</c> value; false on any SDK error in <see cref="FailureMode.Closed"/>.</returns>
    public Task<bool> IsAllowedAsync(
        Subject subject,
        string action,
        string resource,
        IReadOnlyList<SpeedleAttribute>? attributes = null,
        CallOptions? options = null,
        CancellationToken cancellationToken = default) =>
        AllowedAsync(() => attributes, subject, action, resource, options, cancellationToken);

    /// <summary>The detailed form. Always throws on failure, because a decision must come from the server.</summary>
    /// <param name="subject">The subject.</param>
    /// <param name="action">The action.</param>
    /// <param name="resource">The resource.</param>
    /// <param name="attributes">Typed attributes, sent in this order; null means none.</param>
    /// <param name="options">Per-call overrides.</param>
    /// <param name="cancellationToken">Cancels the call.</param>
    /// <returns>The server's decision.</returns>
    public Task<Decision> CheckAsync(
        Subject subject,
        string action,
        string resource,
        IReadOnlyList<SpeedleAttribute>? attributes = null,
        CallOptions? options = null,
        CancellationToken cancellationToken = default) =>
        RunAsync(() => attributes, subject, action, resource, options, cancellationToken);

    /// <summary>
    /// Inferred form: a string becomes <c>string</c>, a numeric primitive <c>numeric</c>, a bool
    /// <c>bool</c>, a <see cref="DateTimeOffset"/> <c>datetime</c>, and a non-empty sequence of one of
    /// these the array form. A string is always <c>string</c>, even if it looks like a date;
    /// <see cref="DateTime"/> is rejected. Attributes are sent sorted by name.
    /// </summary>
    /// <param name="subject">The subject.</param>
    /// <param name="action">The action.</param>
    /// <param name="resource">The resource.</param>
    /// <param name="attributes">Name to native value.</param>
    /// <param name="options">Per-call overrides.</param>
    /// <param name="cancellationToken">Cancels the call.</param>
    /// <returns>See the typed overload.</returns>
    public Task<bool> IsAllowedAsync(
        Subject subject,
        string action,
        string resource,
        IReadOnlyDictionary<string, object?> attributes,
        CallOptions? options = null,
        CancellationToken cancellationToken = default) =>
        AllowedAsync(() => Wire.Infer(attributes), subject, action, resource, options, cancellationToken);

    /// <summary>The detailed form with inferred attributes.</summary>
    /// <param name="subject">The subject.</param>
    /// <param name="action">The action.</param>
    /// <param name="resource">The resource.</param>
    /// <param name="attributes">Name to native value.</param>
    /// <param name="options">Per-call overrides.</param>
    /// <param name="cancellationToken">Cancels the call.</param>
    /// <returns>The server's decision.</returns>
    public Task<Decision> CheckAsync(
        Subject subject,
        string action,
        string resource,
        IReadOnlyDictionary<string, object?> attributes,
        CallOptions? options = null,
        CancellationToken cancellationToken = default) =>
        RunAsync(() => Wire.Infer(attributes), subject, action, resource, options, cancellationToken);

    /// <summary>Releases the connection pool.</summary>
    public void Dispose()
    {
        if (Interlocked.Exchange(ref disposed, 1) == 0)
        {
            transport.Dispose();
        }
    }

    /// <summary>Releases the connection pool.</summary>
    /// <returns>A completed task.</returns>
    public ValueTask DisposeAsync()
    {
        Dispose();
        return ValueTask.CompletedTask;
    }

    /// <inheritdoc/>
    public override string ToString() => $"SpeedleClient[{config.Backend}, {config.Target}]";

    private async Task<bool> AllowedAsync(
        Func<IReadOnlyList<SpeedleAttribute>?> attributes,
        Subject subject,
        string action,
        string resource,
        CallOptions? options,
        CancellationToken cancellationToken)
    {
        try
        {
            Decision d = await RunAsync(attributes, subject, action, resource, options, cancellationToken)
                .ConfigureAwait(false);
            return d.Allowed;
        }
        catch (SpeedleException) when (config.FailureMode == FailureMode.Closed)
        {
            return false;
        }
    }

    private async Task<Decision> RunAsync(
        Func<IReadOnlyList<SpeedleAttribute>?> attributes,
        Subject subject,
        string action,
        string resource,
        CallOptions? options,
        CancellationToken cancellationToken)
    {
        ObjectDisposedException.ThrowIf(Volatile.Read(ref disposed) != 0, this);
        cancellationToken.ThrowIfCancellationRequested();
        byte[] body;
        try
        {
            string service = options?.ServiceName ?? config.ServiceName;
            if (service.Length == 0)
            {
                throw Wire.Invalid("serviceName must not be empty");
            }

            body = Wire.EncodeRequest(subject, service, resource, action, attributes());
        }
        catch (InvalidRequestException e)
        {
            GiveUp(0, GiveUpReason.NonRetryable, e);
            throw;
        }

        bool trial = false;
        if (breaker is not null)
        {
            CircuitBreaker.Admission a = breaker.Acquire(config.Clock.Now);
            BreakerChanged(a.Change);
            if (!a.Admitted)
            {
                var e = new CircuitOpenException(new ErrorInfo("the circuit breaker is open; no request sent"));
                GiveUp(0, GiveUpReason.CircuitOpen, e);
                throw e;
            }

            trial = a.Trial;
        }

        bool recorded = false;
        try
        {
            Attempts result = await AttemptsAsync(body, cancellationToken).ConfigureAwait(false);
            if (result.Decision is not null)
            {
                recorded = true;
                RecordBreaker(CircuitBreaker.Outcome.Success, trial);
                return result.Decision;
            }

            GiveUp(result.Count, result.Reason, result.Error!);
            recorded = true;
            RecordBreaker(BreakerOutcome(result.Error!), trial);
            throw result.Error!;
        }
        finally
        {
            if (!recorded)
            {
                RecordBreaker(CircuitBreaker.Outcome.Neutral, trial); // cancellation or a bug
            }
        }
    }

    private readonly record struct Attempts(Decision? Decision, int Count, GiveUpReason Reason, SpeedleException? Error);

    /// <summary>The retry loop of design §6.3.</summary>
    private async Task<Attempts> AttemptsAsync(byte[] body, CancellationToken cancellationToken)
    {
        IClock clock = config.Clock;
        RetryPolicy retry = config.Retry;
        TimeSpan deadlineAt = clock.Now + retry.Deadline;
        for (int attempt = 1; ; attempt++)
        {
            TimeSpan remaining = deadlineAt - clock.Now;
            if (remaining <= TimeSpan.Zero)
            {
                return new Attempts(
                    null,
                    attempt - 1,
                    GiveUpReason.Deadline,
                    new DeadlineExceededException(new ErrorInfo("the call deadline was exceeded")));
            }

            bool boundIsDeadline = remaining <= retry.AttemptTimeout;
            TimeSpan started = clock.Now;
            Outcome o = await transport
                .SendAsync(body, boundIsDeadline ? remaining : retry.AttemptTimeout, boundIsDeadline, cancellationToken)
                .ConfigureAwait(false);
            int n = attempt;
            Hook(h => h.OnAttempt?.Invoke(new AttemptEvent(n, clock.Now - started, o.Status, o.Error?.GetType().Name)));
            if (o.Decision is not null)
            {
                return new Attempts(o.Decision, attempt, default, null);
            }

            if (o.DeadlineCut)
            {
                return new Attempts(null, attempt, GiveUpReason.Deadline, o.Error);
            }

            if (!o.Retryable)
            {
                return new Attempts(null, attempt, GiveUpReason.NonRetryable, o.Error);
            }

            if (attempt >= retry.MaxAttempts)
            {
                return new Attempts(null, attempt, GiveUpReason.AttemptsExhausted, o.Error);
            }

            TimeSpan delay = Backoff(retry, attempt);
            if (o.RetryAfter is TimeSpan ra && ra > delay)
            {
                delay = ra;
            }

            if (delay >= deadlineAt - clock.Now)
            {
                return new Attempts(
                    null,
                    attempt,
                    o.RetryAfter is not null ? GiveUpReason.RetryAfterExceedsDeadline : GiveUpReason.Deadline,
                    o.Error);
            }

            string errorType = o.Error!.GetType().Name;
            Hook(h => h.OnRetry?.Invoke(new RetryEvent(n + 1, delay, errorType)));
            await clock.DelayAsync(delay, cancellationToken).ConfigureAwait(false);
        }
    }

    /// <summary>Full jitter: random(0, min(MaxDelay, BaseDelay × 2^(n-1))).</summary>
    private TimeSpan Backoff(RetryPolicy retry, int failedAttempt)
    {
        long baseTicks = retry.BaseDelay.Ticks;
        int shift = Math.Min(failedAttempt - 1, 62);
        long exp = baseTicks > (long.MaxValue >> shift) ? long.MaxValue : baseTicks << shift;
        long bound = Math.Min(retry.MaxDelay.Ticks, exp);
        double fraction = Math.Clamp(config.Random(), 0.0, 1.0);
        return TimeSpan.FromTicks((long)(fraction * bound));
    }

    private static CircuitBreaker.Outcome BreakerOutcome(SpeedleException e)
    {
        if (e is NetworkException || e.Status >= 500)
        {
            return CircuitBreaker.Outcome.Failure;
        }

        return e.Status is 200 or (>= 400 and < 500) ? CircuitBreaker.Outcome.Success : CircuitBreaker.Outcome.Neutral;
    }

    private void RecordBreaker(CircuitBreaker.Outcome outcome, bool trial)
    {
        if (breaker is not null)
        {
            BreakerChanged(breaker.Record(outcome, trial, config.Clock.Now));
        }
    }

    private void GiveUp(int attempts, GiveUpReason reason, SpeedleException error) =>
        Hook(h => h.OnGiveUp?.Invoke(new GiveUpEvent(attempts, reason, error)));

    private void BreakerChanged(BreakerStateChangeEvent? e)
    {
        if (e is not null)
        {
            Hook(h => h.OnBreakerStateChange?.Invoke(e));
        }
    }

    private void Hook(Action<SpeedleHooks> call)
    {
        SpeedleHooks? hooks = config.Hooks;
        if (hooks is null)
        {
            return;
        }

        try
        {
            call(hooks);
        }
#pragma warning disable CA1031 // A hook never changes the call's result.
        catch (Exception)
        {
        }
#pragma warning restore CA1031
    }
}
