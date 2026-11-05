// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

// Speedle SDK end-to-end runner for .NET. Runs every vector in e2e/vectors.json
// with SpeedleClient.CheckAsync and fails on any mismatch. The contract is in
// e2e/README.md.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Text.Json;
using System.Threading.Tasks;
using Speedle.Sdk;

internal static class Program
{
    private static async Task<int> Main(string[] args)
    {
        string endpoint = Env("SPEEDLE_E2E_ENDPOINT", true)!;
        string service = Env("SPEEDLE_E2E_SERVICE", true)!;
        string backendName = Env("SPEEDLE_E2E_BACKEND", false) ?? "speedle-cloud";
        string? token = Env("SPEEDLE_E2E_TOKEN", false);
        Backend backend = backendName switch
        {
            "speedle-cloud" => Backend.SpeedleCloud,
            "speedle-plus" => Backend.SpeedlePlus,
            _ => throw Usage("SPEEDLE_E2E_BACKEND must be speedle-cloud or speedle-plus"),
        };
        if (backend == Backend.SpeedleCloud && token is null)
        {
            throw Usage("SPEEDLE_E2E_TOKEN is required for speedle-cloud");
        }
        if (args.Length != 1)
        {
            throw Usage("usage: E2e VECTORS_JSON");
        }

        JsonElement[] vectors;
        try
        {
            using JsonDocument file = JsonDocument.Parse(File.ReadAllText(args[0]));
            vectors = file.RootElement.GetProperty("vectors").EnumerateArray().Select(v => v.Clone()).ToArray();
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or JsonException
                                      or KeyNotFoundException or InvalidOperationException)
        {
            throw Usage($"cannot read {args[0]}: {e.GetType().Name}: {e.Message}");
        }
        if (vectors.Length == 0)
        {
            throw Usage(args[0] + " has no vectors");
        }
        int failed = 0;
        SpeedleClient built;
        try
        {
            built = new SpeedleClient(new SpeedleClientOptions
            {
                Backend = backend,
                Endpoint = new Uri(endpoint),
                Auth = token is null ? Auth.None() : Auth.Bearer(token),
                ServiceName = service,
                UserAgentSuffix = "speedle-sdk-e2e",
            });
        }
        catch (Exception e) when (e is SpeedleException or UriFormatException)
        {
            throw Usage(e.GetType().Name + ": " + e.Message);
        }
        await using (SpeedleClient client = built)
        {
            foreach (JsonElement vector in vectors)
            {
                string name = vector.GetProperty("name").GetString()!;
                string want = Expected(vector);
                string got;
                try
                {
                    got = await RunAsync(client, vector.GetProperty("request")).ConfigureAwait(false);
                }
                catch (Exception e)
                {
                    // SDK errors and malformed vectors alike fail this vector only.
                    got = e.GetType().Name + ": " + e.Message;
                }
                if (want == got)
                {
                    Console.WriteLine("PASS " + name);
                }
                else
                {
                    failed++;
                    Console.WriteLine($"FAIL {name}: want {want}, got {got}");
                }
            }
        }
        Console.WriteLine($"dotnet: {vectors.Length - failed} passed, {failed} failed against {endpoint}");
        return failed == 0 ? 0 : 1;
    }

    private static async Task<string> RunAsync(SpeedleClient client, JsonElement request)
    {
        Principal[] principals = request.GetProperty("subject").GetProperty("principals").EnumerateArray()
            .Select(p => new Principal(
                p.GetProperty("name").GetString()!,
                p.GetProperty("type").GetString()!,
                p.TryGetProperty("idd", out JsonElement idd) ? idd.GetString() : null))
            .ToArray();
        var attributes = new List<SpeedleAttribute>();
        if (request.TryGetProperty("attributes", out JsonElement items))
        {
            attributes.AddRange(items.EnumerateArray().Select(Attribute));
        }
        Decision d = await client.CheckAsync(
            Subject.Of(principals),
            request.GetProperty("action").GetString()!,
            request.GetProperty("resource").GetString()!,
            attributes).ConfigureAwait(false);
        return Format(d.Allowed, (int)d.Reason, d.ErrorMessage);
    }

    private static SpeedleAttribute Attribute(JsonElement a)
    {
        string name = a.GetProperty("name").GetString()!;
        JsonElement value = a.GetProperty("value");
        bool array = value.ValueKind == JsonValueKind.Array;
        JsonElement[] values = array ? value.EnumerateArray().ToArray() : new[] { value };
        switch (a.GetProperty("type").GetString())
        {
            case "string":
                return array
                    ? SpeedleAttribute.Strings(name, values.Select(v => v.GetString()!))
                    : SpeedleAttribute.String(name, values[0].GetString()!);
            case "numeric":
                return array
                    ? SpeedleAttribute.Numerics(name, values.Select(v => v.GetDouble()))
                    : SpeedleAttribute.Numeric(name, values[0].GetDouble());
            case "bool":
                return array
                    ? SpeedleAttribute.Bools(name, values.Select(v => v.GetBoolean()))
                    : SpeedleAttribute.Bool(name, values[0].GetBoolean());
            case "datetime":
                DateTimeOffset[] times = values
                    .Select(v => DateTimeOffset.Parse(v.GetString()!, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind))
                    .ToArray();
                return array ? SpeedleAttribute.Datetimes(name, times) : SpeedleAttribute.Datetime(name, times[0]);
            default:
                throw new InvalidOperationException("unknown attribute type " + a.GetProperty("type"));
        }
    }

    private static string Expected(JsonElement vector)
    {
        if (!vector.TryGetProperty("expect", out JsonElement expect))
        {
            return "no expectation";
        }
        if (expect.ValueKind != JsonValueKind.Object
            || !expect.TryGetProperty("allowed", out JsonElement allowed)
            || allowed.ValueKind is not (JsonValueKind.True or JsonValueKind.False)
            || !expect.TryGetProperty("reason", out JsonElement reason)
            || reason.ValueKind != JsonValueKind.Number
            || !reason.TryGetInt32(out int code))
        {
            return "invalid expectation " + expect.GetRawText();
        }
        string? message = null;
        if (expect.TryGetProperty("errorMessage", out JsonElement m))
        {
            if (m.ValueKind != JsonValueKind.String)
            {
                return "invalid expectation " + expect.GetRawText();
            }
            message = m.GetString();
        }
        return Format(allowed.GetBoolean(), code, message);
    }

    private static string Format(bool allowed, int reason, string? errorMessage)
    {
        string s = $"{{allowed: {(allowed ? "true" : "false")}, reason: {reason}";
        return errorMessage is null ? s + "}" : s + ", errorMessage: " + JsonSerializer.Serialize(errorMessage) + "}";
    }

    private static string? Env(string name, bool required)
    {
        string? value = Environment.GetEnvironmentVariable(name);
        if (string.IsNullOrEmpty(value))
        {
            if (required)
            {
                throw Usage(name + " is required");
            }
            return null;
        }
        return value;
    }

    private static Exception Usage(string message)
    {
        Console.Error.WriteLine("dotnet e2e: " + message);
        Environment.Exit(2);
        return new InvalidOperationException(message);
    }
}
