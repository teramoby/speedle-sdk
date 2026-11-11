// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Security.Cryptography.X509Certificates;

namespace Speedle.Sdk;

/// <summary>
/// How the client authenticates. A closed set of built-in implementations; more may be added
/// later without breaking this type.
/// </summary>
public abstract class Auth
{
    internal Auth() { }

    /// <summary>No credential: self-hosted Speedle on a trusted network or loopback.</summary>
    /// <returns>The auth.</returns>
    public static Auth None() => NoneAuth.Instance;

    /// <summary>
    /// <c>Authorization: Bearer &lt;token&gt;</c>. On Speedle Cloud, <c>token</c> is the account-level
    /// API key; the parameter name is unchanged. Requires an <c>https://</c> endpoint. The API key
    /// is a server-side secret: never ship it to browsers, mobile apps or desktop clients.
    /// </summary>
    /// <param name="token">The API key or gateway credential. Not empty, without whitespace or control characters.</param>
    /// <returns>The auth.</returns>
    /// <exception cref="ConfigurationException">The value is empty or has whitespace or control characters.</exception>
    public static Auth Bearer(string token)
    {
        if (string.IsNullOrEmpty(token))
        {
            throw new ConfigurationException(new ErrorInfo("bearer token must not be empty"));
        }

        foreach (char c in token)
        {
            if (c <= ' ' || char.IsWhiteSpace(c) || char.IsControl(c))
            {
                throw new ConfigurationException(
                    new ErrorInfo("bearer token must not contain whitespace or control characters"));
            }
        }

        return new BearerAuth(token);
    }

    /// <summary>Mutual TLS: presents the client certificate on the TLS handshake.</summary>
    /// <param name="clientCertificate">A certificate with its private key.</param>
    /// <returns>The auth.</returns>
    /// <exception cref="ConfigurationException">The certificate is null or has no private key.</exception>
    public static Auth MutualTls(X509Certificate2 clientCertificate)
    {
        if (clientCertificate is null || !clientCertificate.HasPrivateKey)
        {
            throw new ConfigurationException(
                new ErrorInfo("mutual TLS needs a client certificate with its private key"));
        }

        return new MutualTlsAuth(clientCertificate);
    }
}

internal sealed class NoneAuth : Auth
{
    internal static readonly NoneAuth Instance = new();

    public override string ToString() => "Auth.None()";
}

internal sealed class BearerAuth : Auth
{
    internal BearerAuth(string token) => HeaderValue = "Bearer " + token;

    internal string HeaderValue { get; }

    public override string ToString() => "Auth.Bearer(<redacted>)";
}

internal sealed class MutualTlsAuth : Auth
{
    internal MutualTlsAuth(X509Certificate2 certificate) => Certificate = certificate;

    internal X509Certificate2 Certificate { get; }

    public override string ToString() => "Auth.MutualTls(<redacted>)";
}

/// <summary>
/// Retry, timeout and deadline settings. Before attempt n + 1 the client sleeps a uniformly random
/// delay in [0, min(MaxDelay, BaseDelay × 2^(n-1))], raised to the server's <c>Retry-After</c>.
/// On Speedle Cloud every attempt that reaches the server is charged against the daily quota;
/// <see cref="Disabled"/> turns retries off.
/// </summary>
public sealed record RetryPolicy
{
    /// <summary>Attempts per call, counting the first; 1 disables retries. Default 3.</summary>
    public int MaxAttempts { get; init; } = 3;

    /// <summary>Backoff base. Default 100 ms.</summary>
    public TimeSpan BaseDelay { get; init; } = TimeSpan.FromMilliseconds(100);

    /// <summary>Backoff cap. Default 2 s.</summary>
    public TimeSpan MaxDelay { get; init; } = TimeSpan.FromSeconds(2);

    /// <summary>Bound for one attempt: connect, TLS, request and response. Default 2 s.</summary>
    public TimeSpan AttemptTimeout { get; init; } = TimeSpan.FromSeconds(2);

    /// <summary>Bound for the whole call, attempts and delays included. Default 5 s.</summary>
    public TimeSpan Deadline { get; init; } = TimeSpan.FromSeconds(5);

    /// <summary>One attempt per call; the other values are the defaults.</summary>
    public static RetryPolicy Disabled { get; } = new() { MaxAttempts = 1 };
}

/// <summary>What <c>SpeedleClient.IsAllowedAsync</c> does when a call gives up.</summary>
public enum FailureMode
{
    /// <summary>Default: throw the typed exception.</summary>
    Error,

    /// <summary>
    /// Opt-in fail-closed: <c>IsAllowedAsync</c> returns false instead of throwing any SDK exception.
    /// <c>CheckAsync</c> still throws, and cancellation is never converted. There is no fail-open mode.
    /// </summary>
    Closed,
}

/// <summary>Settings of the optional circuit breaker, which counts calls, not attempts.</summary>
public sealed record CircuitBreakerPolicy
{
    /// <summary>Consecutive failed calls that open the breaker. Default 5.</summary>
    public int FailureThreshold { get; init; } = 5;

    /// <summary>How long the breaker stays open before a half-open trial. Default 30 s.</summary>
    public TimeSpan CoolDown { get; init; } = TimeSpan.FromSeconds(30);
}

/// <summary>Circuit breaker state.</summary>
public enum BreakerState
{
    /// <summary>Calls go through.</summary>
    Closed,

    /// <summary>Calls fail at once with <see cref="CircuitOpenException"/>.</summary>
    Open,

    /// <summary>One trial call is let through.</summary>
    HalfOpen,
}

/// <summary>Why a call ended in an error.</summary>
public enum GiveUpReason
{
    /// <summary>The outcome is not retryable.</summary>
    NonRetryable,

    /// <summary>The last allowed attempt failed with a retryable outcome.</summary>
    AttemptsExhausted,

    /// <summary>The deadline ended an attempt or left no time for another one.</summary>
    Deadline,

    /// <summary>The server's <c>Retry-After</c> does not fit the remaining deadline.</summary>
    RetryAfterExceedsDeadline,

    /// <summary>The circuit breaker rejected the call.</summary>
    CircuitOpen,
}

/// <summary>Reported after every attempt.</summary>
/// <param name="Attempt">The attempt number, from 1.</param>
/// <param name="Duration">How long the attempt took.</param>
/// <param name="Status">The HTTP status, or null.</param>
/// <param name="ErrorType">The exception class name, or null on success.</param>
public sealed record AttemptEvent(int Attempt, TimeSpan Duration, int? Status, string? ErrorType);

/// <summary>Reported before every retry delay.</summary>
/// <param name="Attempt">The number of the next attempt.</param>
/// <param name="Delay">The delay before it.</param>
/// <param name="ErrorType">The exception class name that caused the retry.</param>
public sealed record RetryEvent(int Attempt, TimeSpan Delay, string ErrorType);

/// <summary>Reported when a call ends in an error, in every failure mode.</summary>
/// <param name="Attempts">Attempts made; 0 when no request was sent.</param>
/// <param name="Reason">Why the call gave up.</param>
/// <param name="Error">The exception the call raises, or would raise outside fail-closed mode.</param>
public sealed record GiveUpEvent(int Attempts, GiveUpReason Reason, SpeedleException Error);

/// <summary>Reported when the circuit breaker changes state.</summary>
/// <param name="From">The previous state.</param>
/// <param name="To">The new state.</param>
public sealed record BreakerStateChangeEvent(BreakerState From, BreakerState To);

/// <summary>
/// Optional callbacks, run synchronously on the calling task. Exceptions they throw are caught and
/// ignored. Payloads never contain the token, headers, subject, action, resource, attributes or body.
/// </summary>
public sealed class SpeedleHooks
{
    /// <summary>After every attempt.</summary>
    public Action<AttemptEvent>? OnAttempt { get; init; }

    /// <summary>Before every retry delay.</summary>
    public Action<RetryEvent>? OnRetry { get; init; }

    /// <summary>When a call ends in an error.</summary>
    public Action<GiveUpEvent>? OnGiveUp { get; init; }

    /// <summary>When the circuit breaker changes state.</summary>
    public Action<BreakerStateChangeEvent>? OnBreakerStateChange { get; init; }
}

/// <summary>Client settings, validated by the <see cref="SpeedleClient"/> constructor.</summary>
public sealed class SpeedleClientOptions
{
    /// <summary>The backend.</summary>
    public required Backend Backend { get; init; }

    /// <summary>
    /// The base URL (scheme, host, optional port and path prefix), without query or fragment.
    /// <c>https://</c> is required, except for <see cref="Backend.Speedle"/> with
    /// <see cref="Auth.None"/> on a loopback host, or anywhere with <see cref="AllowInsecureHttp"/>.
    /// </summary>
    public required Uri Endpoint { get; init; }

    /// <summary>The authentication.</summary>
    public required Auth Auth { get; init; }

    /// <summary>
    /// Sent on every call; not empty. Current Speedle Cloud returns 404 <c>service_not_found</c>
    /// for an unknown service. Older Cloud returns 403 <c>service_mismatch</c>.
    /// </summary>
    public required string ServiceName { get; init; }

    /// <summary>Retry, timeout and deadline settings.</summary>
    public RetryPolicy Retry { get; init; } = new();

    /// <summary>The failure mode. Default <see cref="FailureMode.Error"/>.</summary>
    public FailureMode FailureMode { get; init; } = FailureMode.Error;

    /// <summary>The circuit breaker; null means none.</summary>
    public CircuitBreakerPolicy? CircuitBreaker { get; init; }

    /// <summary>Observability hooks, or null.</summary>
    public SpeedleHooks? Hooks { get; init; }

    /// <summary>Extra trust anchors, used in addition to the system roots. Verification cannot be disabled.</summary>
    public X509Certificate2Collection? ExtraTrustedCertificates { get; init; }

    /// <summary>Allows <c>http://</c> to non-loopback hosts for self-hosted Speedle with <see cref="Auth.None"/>.</summary>
    public bool AllowInsecureHttp { get; init; }

    /// <summary>Appended to the <c>User-Agent</c> after a space; printable ASCII.</summary>
    public string? UserAgentSuffix { get; init; }
}

/// <summary>Per-call settings.</summary>
public sealed record CallOptions
{
    /// <summary>Overrides <see cref="SpeedleClientOptions.ServiceName"/> for this call.</summary>
    public string? ServiceName { get; init; }
}

/// <summary>Decision reason codes, the same on both backends. Unknown codes are kept as the cast integer.</summary>
public enum Reason
{
    /// <summary>0: a grant policy matched.</summary>
    GrantPolicyFound = 0,

    /// <summary>1: a deny policy matched.</summary>
    DenyPolicyFound = 1,

    /// <summary>2: the service name is unknown to the server.</summary>
    ServiceNotFound = 2,

    /// <summary>3: no policy applied.</summary>
    NoApplicablePolicies = 3,

    /// <summary>4: evaluation failed; see <see cref="Decision.ErrorMessage"/>.</summary>
    ErrorInEvaluation = 4,

    /// <summary>5: discover mode allows everything. Not a production grant.</summary>
    DiscoverMode = 5,

    /// <summary>6: no reason available.</summary>
    ReasonNotAvailable = 6,
}

/// <summary>The server's answer to one <c>is-allowed</c> call.</summary>
/// <param name="Allowed">The server's <c>allowed</c> value.</param>
/// <param name="Reason">The reason code.</param>
/// <param name="ErrorMessage">The evaluator error (self-hosted Speedle), or null.</param>
/// <param name="QuotaDailyRemaining">Speedle Cloud's <c>X-Quota-Daily-Remaining</c>, or null.</param>
public sealed record Decision(bool Allowed, Reason Reason, string? ErrorMessage, long? QuotaDailyRemaining);
