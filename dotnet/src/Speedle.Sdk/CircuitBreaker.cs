// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Diagnostics;
using System.Threading;
using System.Threading.Tasks;

namespace Speedle.Sdk;

/// <summary>Time source of the retry loop and the breaker; the conformance runner replaces it.</summary>
internal interface IClock
{
    /// <summary>Monotonic time.</summary>
    TimeSpan Now { get; }

    /// <summary>Waits for the delay; cancellable.</summary>
    Task DelayAsync(TimeSpan delay, CancellationToken cancellationToken);
}

internal sealed class SystemClock : IClock
{
    internal static readonly SystemClock Instance = new();

    public TimeSpan Now => Stopwatch.GetElapsedTime(0);

    public Task DelayAsync(TimeSpan delay, CancellationToken cancellationToken) =>
        Task.Delay(delay, cancellationToken);
}

/// <summary>One breaker per client; counts calls, not attempts. Thread-safe.</summary>
internal sealed class CircuitBreaker
{
    internal enum Outcome
    {
        Success,
        Failure,
        Neutral,
    }

    internal readonly record struct Admission(bool Admitted, bool Trial, BreakerStateChangeEvent? Change);

    private readonly object gate = new();
    private readonly int threshold;
    private readonly TimeSpan coolDown;
    private BreakerState state = BreakerState.Closed;
    private int failures;
    private TimeSpan openedAt;
    private bool trialInFlight;

    internal CircuitBreaker(CircuitBreakerPolicy policy)
    {
        threshold = policy.FailureThreshold;
        coolDown = policy.CoolDown;
    }

    internal BreakerState State
    {
        get
        {
            lock (gate)
            {
                return state;
            }
        }
    }

    internal Admission Acquire(TimeSpan now)
    {
        lock (gate)
        {
            switch (state)
            {
                case BreakerState.Closed:
                    return new Admission(true, false, null);
                case BreakerState.Open:
                    if (now - openedAt < coolDown)
                    {
                        return new Admission(false, false, null);
                    }

                    trialInFlight = true;
                    return new Admission(true, true, Move(BreakerState.HalfOpen));
                default:
                    if (trialInFlight)
                    {
                        return new Admission(false, false, null);
                    }

                    trialInFlight = true;
                    return new Admission(true, true, null);
            }
        }
    }

    internal BreakerStateChangeEvent? Record(Outcome outcome, bool trial, TimeSpan now)
    {
        lock (gate)
        {
            if (trial)
            {
                trialInFlight = false;
                if (state != BreakerState.HalfOpen)
                {
                    return null;
                }

                switch (outcome)
                {
                    case Outcome.Success:
                        failures = 0;
                        return Move(BreakerState.Closed);
                    case Outcome.Failure:
                        openedAt = now;
                        return Move(BreakerState.Open);
                    default:
                        return null;
                }
            }

            if (state != BreakerState.Closed)
            {
                return null;
            }

            if (outcome == Outcome.Success)
            {
                failures = 0;
            }
            else if (outcome == Outcome.Failure && ++failures >= threshold)
            {
                failures = 0;
                openedAt = now;
                return Move(BreakerState.Open);
            }

            return null;
        }
    }

    private BreakerStateChangeEvent Move(BreakerState to)
    {
        var e = new BreakerStateChangeEvent(state, to);
        state = to;
        return e;
    }
}
