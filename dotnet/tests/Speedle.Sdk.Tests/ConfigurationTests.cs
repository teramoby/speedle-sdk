// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Text.RegularExpressions;
using Xunit;

namespace Speedle.Sdk.Tests;

public class ConfigurationTests
{
    private static SpeedleClientOptions Plus(string endpoint, Auth? auth = null, bool insecure = false) => new()
    {
        Backend = Backend.SpeedlePlus,
        Endpoint = new Uri(endpoint),
        Auth = auth ?? Auth.None(),
        ServiceName = "books",
        AllowInsecureHttp = insecure,
    };

    private static SpeedleClientOptions Cloud(string endpoint) => new()
    {
        Backend = Backend.SpeedleCloud,
        Endpoint = new Uri(endpoint),
        Auth = Auth.Bearer("spdl_token"),
        ServiceName = "books",
    };

    private static void Ok(SpeedleClientOptions o) => new SpeedleClient(o).Dispose();

    private static void Bad(SpeedleClientOptions o) => Assert.Throws<ConfigurationException>(() => new SpeedleClient(o));

    [Fact]
    public void RequiresAuthAndServiceName()
    {
        Bad(new SpeedleClientOptions { Backend = Backend.SpeedleCloud, Endpoint = new Uri("https://x"), Auth = null!, ServiceName = "b" });
        Bad(new SpeedleClientOptions { Backend = Backend.SpeedleCloud, Endpoint = new Uri("https://x"), Auth = Auth.None(), ServiceName = "" });
        Bad(new SpeedleClientOptions { Backend = Backend.SpeedleCloud, Endpoint = null!, Auth = Auth.None(), ServiceName = "b" });
        Bad(new SpeedleClientOptions { Backend = (Backend)7, Endpoint = new Uri("https://x"), Auth = Auth.None(), ServiceName = "b" });
    }

    [Fact]
    public void EndpointRules()
    {
        Ok(Cloud("https://cloud.speedle.io"));
        Ok(Cloud("https://cloud.speedle.io/prefix/"));
        Bad(Cloud("https://x/?a=1"));
        Bad(Cloud("https://x/#f"));
        Bad(Cloud("https://u:p@x/"));
        Bad(Cloud("ftp://x"));
    }

    [Fact]
    public void PlaintextOnlyForSpeedlePlusWithoutCredentials()
    {
        Ok(Plus("http://127.0.0.1:6734"));
        Ok(Plus("http://localhost:6734"));
        Ok(Plus("http://[::1]:6734"));
        Bad(Plus("http://10.0.0.5:6734"));
        Bad(Plus("http://127.evil.example"));
        Ok(Plus("http://10.0.0.5:6734", insecure: true));
        Bad(Plus("http://127.0.0.1", Auth.Bearer("t"), insecure: true));
        Bad(Cloud("http://127.0.0.1"));
    }

    [Fact]
    public void PolicyValidation()
    {
        var o = Cloud("https://x");
        Bad(new SpeedleClientOptions { Backend = o.Backend, Endpoint = o.Endpoint, Auth = o.Auth, ServiceName = "b", Retry = new RetryPolicy { MaxAttempts = 0 } });
        Bad(new SpeedleClientOptions { Backend = o.Backend, Endpoint = o.Endpoint, Auth = o.Auth, ServiceName = "b", Retry = new RetryPolicy { Deadline = TimeSpan.Zero } });
        Bad(new SpeedleClientOptions { Backend = o.Backend, Endpoint = o.Endpoint, Auth = o.Auth, ServiceName = "b", CircuitBreaker = new CircuitBreakerPolicy { FailureThreshold = 0 } });
        Bad(new SpeedleClientOptions { Backend = o.Backend, Endpoint = o.Endpoint, Auth = o.Auth, ServiceName = "b", UserAgentSuffix = "bad\nsuffix" });
        var d = new RetryPolicy();
        Assert.Equal(3, d.MaxAttempts);
        Assert.Equal(TimeSpan.FromMilliseconds(100), d.BaseDelay);
        Assert.Equal(TimeSpan.FromSeconds(2), d.MaxDelay);
        Assert.Equal(TimeSpan.FromSeconds(2), d.AttemptTimeout);
        Assert.Equal(TimeSpan.FromSeconds(5), d.Deadline);
        Assert.Equal(1, RetryPolicy.Disabled.MaxAttempts);
        Assert.Equal(5, new CircuitBreakerPolicy().FailureThreshold);
        Assert.Equal(TimeSpan.FromSeconds(30), new CircuitBreakerPolicy().CoolDown);
    }

    [Fact]
    public void BearerTokenValidationAndRedaction()
    {
        Assert.Throws<ConfigurationException>(() => Auth.Bearer(""));
        Assert.Throws<ConfigurationException>(() => Auth.Bearer(null!));
        Assert.Throws<ConfigurationException>(() => Auth.Bearer("a b"));
        Assert.Throws<ConfigurationException>(() => Auth.Bearer("a\tb"));
        Assert.Throws<ConfigurationException>(() => Auth.Bearer("ab\n"));
        Assert.Throws<ConfigurationException>(() => Auth.Bearer("a\u00a0b"));
        Assert.DoesNotContain("future", Auth.Bearer("future-format.token").ToString(), StringComparison.Ordinal);
        using var c = new SpeedleClient(Cloud("https://x"));
        Assert.DoesNotContain("spdl_token", c.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public void UserAgent()
    {
        string ua = ClientConfig.BuildUserAgent("myapp/1.2");
        Assert.Matches(new Regex(@"^speedle-sdk-dotnet/0\.1\.0 \(dotnet/[0-9.]+; [a-z]+\) myapp/1\.2$"), ua);
    }

    [Fact]
    public void LoopbackDetection()
    {
        Assert.True(ClientConfig.IsLoopback("127.0.0.1"));
        Assert.True(ClientConfig.IsLoopback("127.1.2.3"));
        Assert.True(ClientConfig.IsLoopback("LOCALHOST"));
        Assert.True(ClientConfig.IsLoopback("[::1]"));
        Assert.False(ClientConfig.IsLoopback("127.1"));
        Assert.False(ClientConfig.IsLoopback("128.0.0.1"));
        Assert.False(ClientConfig.IsLoopback("localhost.example"));
    }
}
