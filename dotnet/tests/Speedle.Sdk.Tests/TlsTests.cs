// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.IO;
using System.Net;
using System.Net.Security;
using System.Net.Sockets;
using System.Security.Authentication;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Xunit;

namespace Speedle.Sdk.Tests;

public sealed class TlsTests : IDisposable
{
    private readonly X509Certificate2 ca;
    private readonly X509Certificate2 serverCert;
    private readonly X509Certificate2 clientCert = Create("CN=speedle-test-client", server: false, issuer: null);
    private readonly TcpListener listener = new(IPAddress.Loopback, 0);
    private readonly CancellationTokenSource stop = new();

    public TlsTests()
    {
        ca = CreateCa();
        serverCert = Create("CN=localhost", server: true, issuer: ca);
        listener.Start();
    }

    public void Dispose()
    {
        stop.Cancel();
        listener.Stop();
        stop.Dispose();
        serverCert.Dispose();
        ca.Dispose();
        clientCert.Dispose();
    }

    private int Port => ((IPEndPoint)listener.LocalEndpoint).Port;

    [Fact]
    public async Task ExtraTrustAnchorAndMutualTls()
    {
        Task server = ServeOnceAsync(requireClientCert: true);
        await using var c = Client(Auth.MutualTls(clientCert), trust: true);
        Assert.True(await c.IsAllowedAsync("alice", "get", "/r"));
        await server;
    }

    [Fact]
    public async Task MissingClientCertificateFailsWithNetworkException()
    {
        _ = ServeOnceAsync(requireClientCert: true);
        await using var c = Client(Auth.None(), trust: true, retry: RetryPolicy.Disabled);
        await Assert.ThrowsAnyAsync<NetworkException>(() => c.IsAllowedAsync("alice", "get", "/r"));
    }

    [Fact]
    public async Task UntrustedServerIsRejectedWithoutRetry()
    {
        _ = ServeOnceAsync(requireClientCert: false);
        int attempts = 0;
        await using var c = Client(
            Auth.None(), trust: false, hooks: new SpeedleHooks { OnAttempt = _ => Interlocked.Increment(ref attempts) });
        NetworkException e = await Assert.ThrowsAsync<NetworkException>(() => c.IsAllowedAsync("alice", "get", "/r"));
        Assert.Equal(1, attempts);
        Assert.Null(e.Status);
    }

    private SpeedleClient Client(Auth auth, bool trust, RetryPolicy? retry = null, SpeedleHooks? hooks = null) =>
        new(new SpeedleClientOptions
        {
            Backend = Backend.Speedle,
            Endpoint = new Uri($"https://localhost:{Port}"),
            Auth = auth,
            ServiceName = "books",
            ExtraTrustedCertificates = trust ? new X509Certificate2Collection(ca) : null,
            Retry = retry ?? new RetryPolicy(),
            Hooks = hooks,
        });

    private async Task ServeOnceAsync(bool requireClientCert)
    {
        using TcpClient tcp = await listener.AcceptTcpClientAsync(stop.Token);
        await using var ssl = new SslStream(tcp.GetStream(), false);
        try
        {
            await ssl.AuthenticateAsServerAsync(
                new SslServerAuthenticationOptions
                {
                    ServerCertificate = serverCert,
                    ClientCertificateRequired = requireClientCert,
                    RemoteCertificateValidationCallback = (_, cert, _, _) =>
                        !requireClientCert || (cert is not null && cert.GetCertHashString() == clientCert.GetCertHashString()),
                },
                stop.Token);
        }
        catch (Exception e) when (e is AuthenticationException or IOException)
        {
            return;
        }

        var reader = new StreamReader(ssl, Encoding.ASCII, false, 1024, leaveOpen: true);
        int length = 0;
        string? line;
        while (!string.IsNullOrEmpty(line = await reader.ReadLineAsync(stop.Token)))
        {
            if (line.StartsWith("Content-Length:", StringComparison.OrdinalIgnoreCase))
            {
                length = int.Parse(line["Content-Length:".Length..].Trim(), System.Globalization.CultureInfo.InvariantCulture);
            }
        }

        char[] body = new char[length];
        int read = 0;
        while (read < length)
        {
            read += await reader.ReadAsync(body.AsMemory(read), stop.Token);
        }

        const string json = "{\"allowed\":true,\"reason\":0}";
        byte[] resp = Encoding.ASCII.GetBytes(
            $"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {json.Length}\r\n\r\n{json}");
        await ssl.WriteAsync(resp, stop.Token);
        await ssl.FlushAsync(stop.Token);
    }

    private static X509Certificate2 CreateCa()
    {
        using var key = ECDsa.Create(ECCurve.NamedCurves.nistP256);
        var req = new CertificateRequest("CN=speedle-test-ca", key, HashAlgorithmName.SHA256);
        req.CertificateExtensions.Add(new X509BasicConstraintsExtension(true, false, 0, true));
        req.CertificateExtensions.Add(new X509KeyUsageExtension(X509KeyUsageFlags.KeyCertSign | X509KeyUsageFlags.CrlSign, true));
        using X509Certificate2 cert = req.CreateSelfSigned(DateTimeOffset.UtcNow.AddMinutes(-5), DateTimeOffset.UtcNow.AddDays(1));
        return Reload(cert);
    }

    private static X509Certificate2 Create(string subject, bool server, X509Certificate2? issuer)
    {
        using var key = ECDsa.Create(ECCurve.NamedCurves.nistP256);
        var req = new CertificateRequest(subject, key, HashAlgorithmName.SHA256);
        req.CertificateExtensions.Add(new X509BasicConstraintsExtension(false, false, 0, true));
        req.CertificateExtensions.Add(new X509KeyUsageExtension(X509KeyUsageFlags.DigitalSignature, true));
        req.CertificateExtensions.Add(new X509EnhancedKeyUsageExtension(
            new OidCollection { new Oid(server ? "1.3.6.1.5.5.7.3.1" : "1.3.6.1.5.5.7.3.2") }, false));
        if (server)
        {
            var san = new SubjectAlternativeNameBuilder();
            san.AddDnsName("localhost");
            san.AddIpAddress(IPAddress.Loopback);
            req.CertificateExtensions.Add(san.Build());
        }

        DateTimeOffset from = DateTimeOffset.UtcNow.AddMinutes(-5);
        // An issued certificate may not outlive its issuer (.NET 10 enforces this).
        DateTimeOffset to = issuer is null ? DateTimeOffset.UtcNow.AddDays(1) : new DateTimeOffset(issuer.NotAfter);
        if (issuer is null)
        {
            using X509Certificate2 self = req.CreateSelfSigned(from, to);
            return Reload(self);
        }

        using X509Certificate2 signed = req.Create(issuer, from, to, RandomNumberGenerator.GetBytes(8));
        using X509Certificate2 withKey = signed.CopyWithPrivateKey(key);
        return Reload(withKey);
    }

    // Round-trip through PFX so the private key is usable by SslStream on every platform.
    private static X509Certificate2 Reload(X509Certificate2 cert)
    {
#pragma warning disable SYSLIB0057 // X509CertificateLoader is .NET 9+; the tests also run on .NET 8.
        return new X509Certificate2(cert.Export(X509ContentType.Pfx));
#pragma warning restore SYSLIB0057
    }
}
