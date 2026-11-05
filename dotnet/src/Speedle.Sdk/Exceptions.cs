// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;

namespace Speedle.Sdk;

/// <summary>Fields shared by every SDK error.</summary>
internal readonly record struct ErrorInfo(
    string Message,
    int? Status = null,
    string? Code = null,
    long? QuotaDailyRemaining = null,
    TimeSpan? RetryAfter = null,
    Exception? Inner = null);

/// <summary>
/// Base type of every error the SDK raises. Messages never contain the bearer token, the request
/// attributes or the request body.
/// </summary>
public class SpeedleException : Exception
{
    internal SpeedleException(ErrorInfo info)
        : base(info.Message, info.Inner)
    {
        Status = info.Status;
        Code = info.Code;
        QuotaDailyRemaining = info.QuotaDailyRemaining;
    }

    /// <summary>The HTTP status of the response that caused this error, or null.</summary>
    public int? Status { get; }

    /// <summary>The Speedle Cloud error code, for example <c>service_mismatch</c>, or null.</summary>
    public string? Code { get; }

    /// <summary>Speedle Cloud's <c>X-Quota-Daily-Remaining</c> header, or null when absent or malformed.</summary>
    public long? QuotaDailyRemaining { get; }
}

/// <summary>Invalid client options, raised by the constructor or an <see cref="Auth"/> factory.</summary>
public sealed class ConfigurationException : SpeedleException
{
    internal ConfigurationException(ErrorInfo info) : base(info) { }
}

/// <summary>HTTP 400, or a client-side check of the request failed and nothing was sent.</summary>
public sealed class InvalidRequestException : SpeedleException
{
    internal InvalidRequestException(ErrorInfo info) : base(info) { }
}

/// <summary>HTTP 401. Never retried.</summary>
public sealed class UnauthorizedException : SpeedleException
{
    internal UnauthorizedException(ErrorInfo info) : base(info) { }
}

/// <summary>HTTP 403, including Speedle Cloud <c>service_mismatch</c>. Never retried.</summary>
public sealed class ForbiddenException : SpeedleException
{
    internal ForbiddenException(ErrorInfo info) : base(info) { }
}

/// <summary>HTTP 413. Never retried.</summary>
public sealed class RequestTooLargeException : SpeedleException
{
    internal RequestTooLargeException(ErrorInfo info) : base(info) { }
}

/// <summary>
/// HTTP 429 with an unknown code or body. Base of <see cref="RateLimitedException"/> and
/// <see cref="QuotaExceededException"/>.
/// </summary>
public class TooManyRequestsException : SpeedleException
{
    internal TooManyRequestsException(ErrorInfo info)
        : base(info)
    {
        RetryAfter = info.RetryAfter;
    }

    /// <summary>The <c>Retry-After</c> header, or null when absent or not a positive integer.</summary>
    public TimeSpan? RetryAfter { get; }
}

/// <summary>A burst HTTP 429. Retried only when <c>Retry-After</c> fits the remaining deadline.</summary>
public sealed class RateLimitedException : TooManyRequestsException
{
    internal RateLimitedException(ErrorInfo info) : base(info) { }
}

/// <summary>HTTP 429 <c>daily_quota_exceeded</c> from Speedle Cloud. Never retried.</summary>
public sealed class QuotaExceededException : TooManyRequestsException
{
    internal QuotaExceededException(ErrorInfo info) : base(info) { }
}

/// <summary>HTTP 503. Retried while attempts and the deadline remain.</summary>
public sealed class ServiceUnavailableException : SpeedleException
{
    internal ServiceUnavailableException(ErrorInfo info) : base(info) { }
}

/// <summary>HTTP 500 and other 5xx; 502 and 504 are retried, the others are not.</summary>
public sealed class ServerException : SpeedleException
{
    internal ServerException(ErrorInfo info) : base(info) { }
}

/// <summary>Any other HTTP status, including 3xx (redirects are never followed) and 404.</summary>
public sealed class HttpException : SpeedleException
{
    internal HttpException(ErrorInfo info) : base(info) { }
}

/// <summary>The response body is not the expected JSON.</summary>
public sealed class UnexpectedResponseException : SpeedleException
{
    internal UnexpectedResponseException(ErrorInfo info) : base(info) { }
}

/// <summary>A transport failure. TLS failures are not retried; the others are.</summary>
public class NetworkException : SpeedleException
{
    internal NetworkException(ErrorInfo info) : base(info) { }
}

/// <summary>The last attempt hit the per-attempt timeout.</summary>
public class SpeedleTimeoutException : NetworkException
{
    internal SpeedleTimeoutException(ErrorInfo info) : base(info) { }
}

/// <summary>The call's deadline ended an attempt or left no time to start one.</summary>
public sealed class DeadlineExceededException : SpeedleTimeoutException
{
    internal DeadlineExceededException(ErrorInfo info) : base(info) { }
}

/// <summary>The circuit breaker rejected the call; no request was sent.</summary>
public sealed class CircuitOpenException : SpeedleException
{
    internal CircuitOpenException(ErrorInfo info) : base(info) { }
}
