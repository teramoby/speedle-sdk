// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Diagnostics;
using System.IO;
using System.Net.Http;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Text.Json.Nodes;
using System.Threading.Tasks;

namespace Speedle.Sdk.Tests;

/// <summary>
/// The shared conformance mock in <c>conformance/mock</c>, driven through its control API. Started
/// once per test run: the binary in <c>SPEEDLE_MOCK</c>, otherwise <c>go run</c>.
/// </summary>
public sealed class SharedMock : IDisposable
{
    private readonly Process process;
    private readonly HttpClient control = new() { Timeout = TimeSpan.FromSeconds(10) };
    private readonly Uri controlUri;

    public SharedMock()
    {
        string root = RepoRoot();
        string? bin = Environment.GetEnvironmentVariable("SPEEDLE_MOCK");
        var psi = string.IsNullOrEmpty(bin)
            ? new ProcessStartInfo("go") { WorkingDirectory = Path.Combine(root, "conformance", "mock") }
            : new ProcessStartInfo(bin);
        if (string.IsNullOrEmpty(bin))
        {
            psi.ArgumentList.Add("run");
            psi.ArgumentList.Add(".");
        }

        psi.ArgumentList.Add("-exit-on-stdin-close");
        psi.RedirectStandardInput = true;
        psi.RedirectStandardOutput = true;
        psi.UseShellExecute = false;
        process = Process.Start(psi) ?? throw new InvalidOperationException("cannot start conformance/mock");
        string line = process.StandardOutput.ReadLine()
            ?? throw new InvalidOperationException("conformance/mock exited before printing its addresses");
        JsonNode info = JsonNode.Parse(line)!;
        Endpoint = new Uri((string)info["endpoint"]!);
        controlUri = new Uri((string)info["control"]!);
        Ca = X509Certificate2.CreateFromPem((string)info["caPem"]!);
        VectorDir = Path.Combine(root, "conformance", "vectors");
    }

    public Uri Endpoint { get; }

    public X509Certificate2 Ca { get; }

    public string VectorDir { get; }

    public static string RepoRoot()
    {
        for (var d = new DirectoryInfo(AppContext.BaseDirectory); d is not null; d = d.Parent)
        {
            if (Directory.Exists(Path.Combine(d.FullName, "conformance", "vectors")))
            {
                return d.FullName;
            }
        }

        throw new DirectoryNotFoundException("conformance/vectors not found above " + AppContext.BaseDirectory);
    }

    public async Task LoadAsync(string vector)
    {
        using var content = new StringContent(vector, Encoding.UTF8, "application/json");
        using HttpResponseMessage r = await control.PostAsync(new Uri(controlUri, "/load"), content);
        if ((int)r.StatusCode != 204)
        {
            throw new InvalidOperationException($"mock rejected the vector: {(int)r.StatusCode} {await r.Content.ReadAsStringAsync()}");
        }
    }

    /// <summary>Null when every exchange was consumed and matched, otherwise the report.</summary>
    public async Task<string?> VerifyAsync()
    {
        string body = await control.GetStringAsync(new Uri(controlUri, "/verify"));
        return JsonNode.Parse(body)?["ok"]?.GetValue<bool>() == true ? null : body;
    }

    public void Dispose()
    {
        try
        {
            using var empty = new StringContent(string.Empty);
            control.PostAsync(new Uri(controlUri, "/shutdown"), empty).GetAwaiter().GetResult().Dispose();
        }
        catch (HttpRequestException)
        {
            // already gone
        }

        process.StandardInput.Close();
        if (!process.WaitForExit(5000))
        {
            process.Kill(entireProcessTree: true);
        }

        process.Dispose();
        control.Dispose();
        Ca.Dispose();
    }
}
