// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Globalization;
using System.Linq;
using System.Net;
using System.Reflection;

namespace Speedle.Sdk;

/// <summary>A validated client configuration.</summary>
internal sealed record ClientConfig(
    Backend Backend,
    Uri Target,
    Auth Auth,
    string ServiceName,
    RetryPolicy Retry,
    FailureMode FailureMode,
    CircuitBreakerPolicy? CircuitBreaker,
    SpeedleHooks? Hooks,
    string UserAgent,
    IClock Clock,
    Func<double> Random)
{
    internal static readonly string Version = LoadVersion();

    internal static ClientConfig Validate(SpeedleClientOptions o, IClock clock, Func<double> random)
    {
        if (o is null)
        {
            throw Config("options are required");
        }

        if (!Enum.IsDefined(o.Backend))
        {
            throw Config("backend is invalid");
        }

        if (o.Auth is null)
        {
            throw Config("auth is required");
        }

        if (string.IsNullOrEmpty(o.ServiceName))
        {
            throw Config("serviceName is required and must not be empty");
        }

        Uri target = Target(o.Endpoint);
        if (target.Scheme == Uri.UriSchemeHttp)
        {
            if (o.Backend != Backend.SpeedlePlus || o.Auth is not NoneAuth)
            {
                throw Config("http:// is allowed only for SpeedlePlus with Auth.None(); use https://");
            }

            if (!o.AllowInsecureHttp && !IsLoopback(target.Host))
            {
                throw Config("http:// is allowed only for loopback hosts unless AllowInsecureHttp is set");
            }
        }

        RetryPolicy retry = o.Retry ?? new RetryPolicy();
        if (retry.MaxAttempts < 1)
        {
            throw Config("Retry.MaxAttempts must be at least 1");
        }

        if (retry.BaseDelay < TimeSpan.Zero || retry.MaxDelay < TimeSpan.Zero)
        {
            throw Config("Retry delays must not be negative");
        }

        if (retry.AttemptTimeout <= TimeSpan.Zero || retry.Deadline <= TimeSpan.Zero)
        {
            throw Config("Retry.AttemptTimeout and Retry.Deadline must be positive");
        }

        if (o.CircuitBreaker is { } cb && (cb.FailureThreshold < 1 || cb.CoolDown <= TimeSpan.Zero))
        {
            throw Config("CircuitBreaker.FailureThreshold must be at least 1 and CoolDown positive");
        }

        if (!Enum.IsDefined(o.FailureMode))
        {
            throw Config("failure mode is invalid");
        }

        if (o.UserAgentSuffix is { } s && s.Any(c => c < 0x20 || c > 0x7e))
        {
            throw Config("UserAgentSuffix must be printable ASCII");
        }

        return new ClientConfig(
            o.Backend,
            target,
            o.Auth,
            o.ServiceName,
            retry,
            o.FailureMode,
            o.CircuitBreaker,
            o.Hooks,
            BuildUserAgent(o.UserAgentSuffix),
            clock,
            random);
    }

    private static Uri Target(Uri? endpoint)
    {
        if (endpoint is null)
        {
            throw Config("endpoint is required");
        }

        if (!endpoint.IsAbsoluteUri || (endpoint.Scheme != Uri.UriSchemeHttps && endpoint.Scheme != Uri.UriSchemeHttp))
        {
            throw Config("endpoint must be an absolute https:// URL");
        }

        if (string.IsNullOrEmpty(endpoint.Host))
        {
            throw Config("endpoint must have a host");
        }

        if (endpoint.Query.Length > 0 || endpoint.Fragment.Length > 0 || endpoint.OriginalString.Contains('#', StringComparison.Ordinal))
        {
            throw Config("endpoint must not have a query or fragment");
        }

        if (endpoint.UserInfo.Length > 0)
        {
            throw Config("endpoint must not contain user info");
        }

        string path = endpoint.AbsolutePath.TrimEnd('/');
        var b = new UriBuilder(endpoint) { Path = path + Wire.Path, Query = string.Empty, Fragment = string.Empty };
        return b.Uri;
    }

    internal static bool IsLoopback(string host)
    {
        string h = host.Trim('[', ']');
        if (h.Equals("localhost", StringComparison.OrdinalIgnoreCase))
        {
            return true;
        }

        return IPAddress.TryParse(h, out IPAddress? ip) && IPAddress.IsLoopback(ip) && LooksLikeIp(h);
    }

    // IPAddress.TryParse accepts forms like "127.1"; require a full dotted quad or an IPv6 literal.
    private static bool LooksLikeIp(string h) => h.Contains(':', StringComparison.Ordinal) || h.Count(c => c == '.') == 3;

    internal static string BuildUserAgent(string? suffix)
    {
        string os = OperatingSystem.IsLinux() ? "linux"
            : OperatingSystem.IsMacOS() ? "darwin"
            : OperatingSystem.IsWindows() ? "windows"
            : OperatingSystem.IsFreeBSD() ? "freebsd"
            : "unknown";
        string ua = string.Create(
            CultureInfo.InvariantCulture, $"speedle-sdk-dotnet/{Version} (dotnet/{Environment.Version}; {os})");
        return string.IsNullOrEmpty(suffix) ? ua : ua + " " + suffix;
    }

    private static string LoadVersion()
    {
        string? v = typeof(ClientConfig).Assembly
            .GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion;
        if (string.IsNullOrEmpty(v))
        {
            return "0.0.0";
        }

        int plus = v.IndexOf('+', StringComparison.Ordinal);
        return plus >= 0 ? v[..plus] : v;
    }

    private static ConfigurationException Config(string message) => new(new ErrorInfo(message));
}
