// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Security.Cryptography.X509Certificates;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Threading.Tasks;
using Xunit;

namespace Speedle.Sdk.Tests;

[CollectionDefinition("conformance")]
public class ConformanceCollection : ICollectionFixture<SharedMock>
{
}

/// <summary>Runs every vector in <c>conformance/vectors</c> against <c>conformance/mock</c> (design §11).</summary>
[Collection("conformance")]
[Trait("Category", "Conformance")]
public class ConformanceTests
{
    private readonly SharedMock mock;

    public ConformanceTests(SharedMock mock) => this.mock = mock;

    public static IEnumerable<object[]> Vectors() =>
        Directory.GetFiles(Path.Combine(SharedMock.RepoRoot(), "conformance", "vectors"), "*.json")
            .Select(f => Path.GetFileName(f))
            .Order(StringComparer.Ordinal)
            .Select(f => new object[] { f! });

    [Theory]
    [MemberData(nameof(Vectors))]
    public async Task Vector(string file)
    {
        string text = await File.ReadAllTextAsync(Path.Combine(mock.VectorDir, file));
        JsonObject v = JsonNode.Parse(text)!.AsObject();
        Assert.Equal(Path.GetFileNameWithoutExtension(file), (string?)v["id"]);
        await mock.LoadAsync(text);

        var clock = new TestClock();
        var recorder = new Recorder();
        double jitter = v.ContainsKey("jitterFraction") ? v["jitterFraction"]!.GetValue<double>() : 1.0;
        JsonObject config = v["config"]!.AsObject();
        using var client = new SpeedleClient(Options(v, config, recorder), clock, () => jitter, null);

        JsonArray steps = v.ContainsKey("steps")
            ? v["steps"]!.AsArray()
            : new JsonArray(new JsonObject { ["call"] = v["call"]!.DeepClone(), ["expect"] = v["expect"]!.DeepClone() });
        int n = 0;
        foreach (JsonNode? s in steps)
        {
            JsonObject step = s!.AsObject();
            n++;
            if (step.ContainsKey("advanceMs"))
            {
                clock.Advance(TimeSpan.FromMilliseconds(step["advanceMs"]!.GetValue<long>()));
            }

            clock.TakeSleepsMs();
            object result;
            try
            {
                result = await Call(client, step["call"]!.AsObject());
            }
            catch (SpeedleException e)
            {
                result = e;
            }

            CheckOutcome($"step {n}", step["expect"]!.AsObject(), result, clock.TakeSleepsMs());
        }

        Assert.Null(await mock.VerifyAsync());
        if (v.ContainsKey("events"))
        {
            JsonArray want = v["events"]!.AsArray();
            if (want.Count != recorder.Events.Count)
            {
                Assert.Fail($"events: got {JsonSerializer.Serialize(recorder.Events)}");
            }
            for (int i = 0; i < want.Count; i++)
            {
                JsonObject w = want[i]!.AsObject();
                foreach (KeyValuePair<string, JsonNode?> f in w)
                {
                    CheckField($"event {i + 1}", w, f.Key, recorder.Events[i].GetValueOrDefault(f.Key));
                }
            }
        }
    }

    private SpeedleClientOptions Options(JsonObject v, JsonObject config, Recorder recorder)
    {
        JsonObject auth = config["auth"]!.AsObject();
        var retry = new RetryPolicy();
        if (config["retry"] is JsonObject r)
        {
            retry = new RetryPolicy
            {
                MaxAttempts = r["maxAttempts"]?.GetValue<int>() ?? retry.MaxAttempts,
                BaseDelay = Ms(r, "baseDelayMs") ?? retry.BaseDelay,
                MaxDelay = Ms(r, "maxDelayMs") ?? retry.MaxDelay,
                AttemptTimeout = Ms(r, "attemptTimeoutMs") ?? retry.AttemptTimeout,
                Deadline = Ms(r, "deadlineMs") ?? retry.Deadline,
            };
        }

        CircuitBreakerPolicy? breaker = null;
        if (config["circuitBreaker"] is JsonObject cb)
        {
            var d = new CircuitBreakerPolicy();
            breaker = new CircuitBreakerPolicy
            {
                FailureThreshold = cb["failureThreshold"]?.GetValue<int>() ?? d.FailureThreshold,
                CoolDown = Ms(cb, "coolDownMs") ?? d.CoolDown,
            };
        }

        return new SpeedleClientOptions
        {
            Backend = (string?)v["backend"] == "speedle-cloud" ? Backend.SpeedleCloud : Backend.SpeedlePlus,
            Endpoint = mock.Endpoint,
            Auth = (string?)auth["type"] == "bearer" ? Auth.Bearer((string)auth["token"]!) : Auth.None(),
            ServiceName = (string)config["serviceName"]!,
            Retry = retry,
            FailureMode = (string?)config["failureMode"] == "closed" ? FailureMode.Closed : FailureMode.Error,
            CircuitBreaker = breaker,
            Hooks = recorder.Hooks,
            ExtraTrustedCertificates = new X509Certificate2Collection(mock.Ca),
        };
    }

    private static TimeSpan? Ms(JsonObject o, string key) =>
        o[key] is JsonNode n ? TimeSpan.FromMilliseconds(n.GetValue<long>()) : null;

    private static async Task<object> Call(SpeedleClient client, JsonObject call)
    {
        Subject subject = call["subject"] is JsonArray ps
            ? Subject.Of(ps.Select(p => new Principal((string)p!["name"]!, (string?)p["type"] ?? "user", (string?)p["idd"])).ToArray())
            : (string)call["subject"]!;
        string action = (string)call["action"]!;
        string resource = (string)call["resource"]!;
        CallOptions? options = call.ContainsKey("serviceName") ? new CallOptions { ServiceName = (string?)call["serviceName"] } : null;
        bool isAllowed = (string?)call["method"] == "isAllowed";
        if (call.ContainsKey("inferredAttributes"))
        {
            var attrs = (Dictionary<string, object?>)ToNative(call["inferredAttributes"])!;
            return isAllowed
                ? await client.IsAllowedAsync(subject, action, resource, attrs, options)
                : await client.CheckAsync(subject, action, resource, attrs, options);
        }

        var typed = new List<SpeedleAttribute>();
        if (call["attributes"] is JsonArray list)
        {
            foreach (JsonNode? a in list)
            {
                typed.Add(Attribute(a!.AsObject()));
            }
        }

        return isAllowed
            ? await client.IsAllowedAsync(subject, action, resource, typed, options)
            : await client.CheckAsync(subject, action, resource, typed, options);
    }

    private static SpeedleAttribute Attribute(JsonObject a)
    {
        string name = (string)a["name"]!;
        JsonNode value = a["value"]!;
        static DateTimeOffset Time(JsonNode n) =>
            DateTimeOffset.Parse((string)n!, CultureInfo.InvariantCulture, DateTimeStyles.AssumeUniversal);
        bool array = value is JsonArray;
        return (string?)a["type"] switch
        {
            "string" => array ? SpeedleAttribute.Strings(name, value.AsArray().Select(x => (string)x!)) : SpeedleAttribute.String(name, (string)value!),
            "numeric" => array ? SpeedleAttribute.Numerics(name, value.AsArray().Select(x => x!.GetValue<double>())) : SpeedleAttribute.Numeric(name, value.GetValue<double>()),
            "bool" => array ? SpeedleAttribute.Bools(name, value.AsArray().Select(x => x!.GetValue<bool>())) : SpeedleAttribute.Bool(name, value.GetValue<bool>()),
            "datetime" => array ? SpeedleAttribute.Datetimes(name, value.AsArray().Select(x => Time(x!))) : SpeedleAttribute.Datetime(name, Time(value)),
            _ => throw new InvalidOperationException("unknown attribute type in vector"),
        };
    }

    /// <summary>JSON to the native values a caller would pass to the inferred overload.</summary>
    private static object? ToNative(JsonNode? n)
    {
        switch (n)
        {
            case null:
                return null;
            case JsonObject o:
                return o.ToDictionary(kv => kv.Key, kv => ToNative(kv.Value));
            case JsonArray a:
                return a.Select(ToNative).ToList();
        }

        JsonElement e = n.GetValue<JsonElement>();
        return e.ValueKind switch
        {
            JsonValueKind.String => e.GetString(),
            JsonValueKind.True => true,
            JsonValueKind.False => false,
            JsonValueKind.Number when e.TryGetInt64(out long l) => l,
            JsonValueKind.Number => e.GetDouble(),
            _ => null,
        };
    }

    private static void CheckOutcome(string where, JsonObject expect, object result, List<long> sleeps)
    {
        if (expect["delaysMs"] is JsonArray delays)
        {
            Assert.Equal(delays.Select(d => d!.GetValue<long>()).ToList(), sleeps);
        }

        if (expect.ContainsKey("result"))
        {
            if (result is not bool b)
            {
                Assert.Fail($"{where}: expected a result, got {Describe(result)}");
                return;
            }

            Assert.Equal(expect["result"]!.GetValue<bool>(), b);
        }
        else if (expect["decision"] is JsonObject want)
        {
            Decision d = Assert.IsType<Decision>(result);
            Assert.Equal(want["allowed"]!.GetValue<bool>(), d.Allowed);
            Assert.Equal(want["reason"]!.GetValue<int>(), (int)d.Reason);
            CheckField(where, want, "errorMessage", d.ErrorMessage);
            CheckField(where, want, "quotaDailyRemaining", d.QuotaDailyRemaining);
        }
        else
        {
            JsonObject err = expect["error"]!.AsObject();
            if (result is not SpeedleException e)
            {
                Assert.Fail($"{where}: expected {err["type"]}, got {Describe(result)}");
                return;
            }

            Assert.Equal((string?)err["type"], Names.Error(e.GetType().Name));
            CheckField(where, err, "status", e.Status);
            CheckField(where, err, "code", e.Code);
            CheckField(where, err, "retryAfterSeconds", (e as TooManyRequestsException)?.RetryAfter?.TotalSeconds);
            CheckField(where, err, "quotaDailyRemaining", e.QuotaDailyRemaining);
        }
    }

    private static void CheckField(string where, JsonObject want, string key, object? got)
    {
        if (!want.ContainsKey(key))
        {
            return;
        }

        JsonNode? w = want[key];
        if (w is null)
        {
            if (got is not null)
            {
                Assert.Fail($"{where}: {key} must be empty, got {got}");
            }

            return;
        }

        JsonElement e = w.GetValue<JsonElement>();
        switch (e.ValueKind)
        {
            case JsonValueKind.Number:
                if (got is null || Convert.ToDecimal(got, CultureInfo.InvariantCulture) != e.GetDecimal())
                {
                    Assert.Fail($"{where}: {key} {got} != {e}");
                }

                break;
            case JsonValueKind.String:
                Assert.Equal(e.GetString(), got as string);
                break;
            default:
                Assert.Equal(e.ValueKind == JsonValueKind.True, got);
                break;
        }
    }

    private static string Describe(object r) => r is Exception e ? e.GetType().Name : Convert.ToString(r, CultureInfo.InvariantCulture) ?? "null";
}
