// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Net.Security;
using System.Security.Authentication;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Threading;
using System.Threading.Tasks;

namespace Speedle.Sdk;

/// <summary>The result of one attempt: a decision, or an error with its retry classification.</summary>
internal readonly record struct Outcome(
    Decision? Decision,
    SpeedleException? Error,
    int? Status,
    bool Retryable,
    TimeSpan? RetryAfter,
    bool DeadlineCut);

/// <summary>One attempt over a shared keep-alive <see cref="HttpClient"/>. Never follows redirects.</summary>
internal sealed class Transport : IDisposable
{
    private static readonly MediaTypeHeaderValue Json = new("application/json");

    private readonly HttpClient http;
    private readonly Uri target;
    private readonly Backend backend;
    private readonly string? authorization;
    private readonly string userAgent;

    internal Transport(
        Uri target,
        Backend backend,
        Auth auth,
        X509Certificate2Collection? extraTrust,
        string userAgent,
        HttpMessageHandler? handler)
    {
        this.target = target;
        this.backend = backend;
        this.userAgent = userAgent;
        authorization = auth is BearerAuth b ? b.HeaderValue : null;
        http = new HttpClient(handler ?? CreateHandler(auth, extraTrust), disposeHandler: true)
        {
            Timeout = Timeout.InfiniteTimeSpan,
        };
    }

    private static SocketsHttpHandler CreateHandler(Auth auth, X509Certificate2Collection? extraTrust)
    {
        var ssl = new SslClientAuthenticationOptions();
        if (extraTrust is { Count: > 0 })
        {
            X509Certificate2[] anchors = extraTrust.Cast<X509Certificate2>().ToArray();
            ssl.RemoteCertificateValidationCallback = (_, cert, chain, errors) =>
                errors == SslPolicyErrors.None
                || (errors == SslPolicyErrors.RemoteCertificateChainErrors
                    && cert is not null
                    && TrustedByExtra(cert, chain, anchors));
        }

        if (auth is MutualTlsAuth m)
        {
            ssl.ClientCertificates = new X509CertificateCollection { m.Certificate };
            ssl.LocalCertificateSelectionCallback = (_, _, _, _, _) => m.Certificate;
        }

        return new SocketsHttpHandler
        {
            AllowAutoRedirect = false,
            UseCookies = false,
            AutomaticDecompression = DecompressionMethods.None,
            PooledConnectionLifetime = TimeSpan.FromMinutes(5),
            SslOptions = ssl,
        };
    }

    private static bool TrustedByExtra(X509Certificate cert, X509Chain? presented, X509Certificate2[] anchors)
    {
        using var chain = new X509Chain();
        chain.ChainPolicy.TrustMode = X509ChainTrustMode.CustomRootTrust;
        chain.ChainPolicy.CustomTrustStore.AddRange(anchors);
        chain.ChainPolicy.RevocationMode = X509RevocationMode.NoCheck;
        chain.ChainPolicy.ApplicationPolicy.Add(new Oid("1.3.6.1.5.5.7.3.1")); // server authentication
        if (presented is not null)
        {
            foreach (X509ChainElement e in presented.ChainElements)
            {
                chain.ChainPolicy.ExtraStore.Add(e.Certificate);
            }
        }

        X509Certificate2 leaf = cert as X509Certificate2 ?? new X509Certificate2(cert);
        return chain.Build(leaf);
    }

    /// <summary>Sends one request. Caller cancellation propagates as <see cref="OperationCanceledException"/>.</summary>
    internal async Task<Outcome> SendAsync(
        byte[] body, TimeSpan timeout, bool boundIsDeadline, CancellationToken cancellationToken)
    {
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        cts.CancelAfter(timeout < TimeSpan.FromMilliseconds(1) ? TimeSpan.FromMilliseconds(1) : timeout);
        using var content = new ByteArrayContent(body);
        content.Headers.ContentType = Json;
        using var request = new HttpRequestMessage(HttpMethod.Post, target)
        {
            Version = HttpVersion.Version11,
            VersionPolicy = HttpVersionPolicy.RequestVersionExact,
            Content = content,
        };
        request.Headers.TryAddWithoutValidation("User-Agent", userAgent);
        request.Headers.TryAddWithoutValidation("Accept", "application/json");
        if (authorization is not null)
        {
            request.Headers.TryAddWithoutValidation("Authorization", authorization);
        }

        try
        {
            using HttpResponseMessage response = await http
                .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cts.Token)
                .ConfigureAwait(false);
            byte[] bytes = await response.Content.ReadAsByteArrayAsync(cts.Token).ConfigureAwait(false);
            return Responses.Classify(
                backend,
                (int)response.StatusCode,
                bytes,
                Header(response, "X-Quota-Daily-Remaining"),
                Header(response, "Retry-After"));
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw new OperationCanceledException(cancellationToken);
        }
        catch (OperationCanceledException e)
        {
            return TimedOut(boundIsDeadline, e);
        }
        catch (HttpRequestException e)
        {
            return Failure(e);
        }
        catch (IOException e)
        {
            return Failure(e);
        }
    }

    private static string? Header(HttpResponseMessage r, string name)
    {
        if (r.Headers.TryGetValues(name, out IEnumerable<string>? v))
        {
            return v.FirstOrDefault();
        }

        return r.Content.Headers.TryGetValues(name, out IEnumerable<string>? c) ? c.FirstOrDefault() : null;
    }

    private static Outcome Failure(Exception e)
    {
        bool tls = e is HttpRequestException { HttpRequestError: HttpRequestError.SecureConnectionError };
        for (Exception? x = e; x is not null && !tls; x = x.InnerException)
        {
            tls = x is AuthenticationException;
        }

        if (tls)
        {
            return new Outcome(
                null, new NetworkException(new ErrorInfo("TLS handshake or certificate failure", Inner: e)), null, false, null, false);
        }

        return new Outcome(
            null, new NetworkException(new ErrorInfo($"transport failure ({e.GetType().Name})", Inner: e)), null, true, null, false);
    }

    private static Outcome TimedOut(bool boundIsDeadline, Exception e) =>
        boundIsDeadline
            ? new Outcome(null, new DeadlineExceededException(new ErrorInfo("the call deadline was exceeded", Inner: e)), null, false, null, true)
            : new Outcome(null, new SpeedleTimeoutException(new ErrorInfo("the attempt timed out", Inner: e)), null, true, null, false);

    public void Dispose() => http.Dispose();
}

/// <summary>Maps HTTP responses to decisions and typed errors (design §3.4).</summary>
internal static class Responses
{
    internal static Outcome Classify(Backend backend, int status, byte[] body, string? quotaHeader, string? retryAfterHeader)
    {
        long? quota = Wire.ParseQuota(quotaHeader);
        if (status == 200)
        {
            Decision? d = Wire.DecodeDecision(body, quota);
            return d is not null
                ? new Outcome(d, null, 200, false, null, false)
                : new Outcome(
                    null,
                    new UnexpectedResponseException(Info(status, null, "the response is not a valid decision", quota, null)),
                    200,
                    false,
                    null,
                    false);
        }

        string? error = Wire.ErrorField(body);
        string? code = null;
        string? message = null;
        if (error is not null)
        {
            if (backend == Backend.SpeedleCloud && Wire.CloudCodes.Contains(error))
            {
                code = error;
            }
            else
            {
                message = Wire.Sanitize(error);
            }
        }

        TimeSpan? retryAfter = Wire.ParseRetryAfter(retryAfterHeader);
        ErrorInfo info = Info(status, code, message, quota, retryAfter);
        bool retryable = false;
        SpeedleException e;
        switch (status)
        {
            case 400:
                e = new InvalidRequestException(info);
                break;
            case 401:
                e = new UnauthorizedException(info);
                break;
            case 403:
                e = new ForbiddenException(info);
                break;
            case 413:
                e = new RequestTooLargeException(info);
                break;
            case 429 when backend == Backend.SpeedlePlus || code == "rate_limited":
                e = new RateLimitedException(info);
                retryable = retryAfter is not null;
                break;
            case 429 when code == "daily_quota_exceeded":
                e = new QuotaExceededException(info);
                break;
            case 429:
                e = new TooManyRequestsException(info);
                break;
            case 503:
                e = new ServiceUnavailableException(info);
                retryable = true;
                break;
            case 502:
            case 504:
                e = new ServerException(info);
                retryable = true;
                break;
            case >= 500 and <= 599:
                e = new ServerException(info);
                break;
            default:
                e = new HttpException(info);
                break;
        }

        return new Outcome(null, e, status, retryable, retryable ? retryAfter : null, false);
    }

    private static ErrorInfo Info(int status, string? code, string? text, long? quota, TimeSpan? retryAfter)
    {
        string m = $"is-allowed failed with HTTP {status}";
        if (code is not null)
        {
            m += ": " + code;
        }
        else if (text is not null)
        {
            m += ": " + text;
        }

        return new ErrorInfo(m, status, code, quota, retryAfter);
    }
}
