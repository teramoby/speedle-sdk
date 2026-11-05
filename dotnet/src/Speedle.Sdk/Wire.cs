// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Buffers;
using System.Collections;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Speedle.Sdk;

/// <summary>Request encoding and response decoding for <c>POST /authz-check/v1/is-allowed</c>.</summary>
internal static class Wire
{
    internal const string Path = "/authz-check/v1/is-allowed";

    /// <summary>The Speedle Cloud contract's error codes; other <c>error</c> values are messages.</summary>
    internal static readonly HashSet<string> CloudCodes = new(StringComparer.Ordinal)
    {
        "invalid_request", "unsupported_token_assertion", "unauthorized", "service_mismatch",
        "request_too_large", "rate_limited", "daily_quota_exceeded", "internal_error",
        "quota_unavailable", "snapshot_unavailable",
    };

    private const int MaxMessage = 512;

    internal static string TypeName(AttributeType t) => t switch
    {
        AttributeType.String => "string",
        AttributeType.Numeric => "numeric",
        AttributeType.Bool => "bool",
        _ => "datetime",
    };

    internal static InvalidRequestException Invalid(string message) => new(new ErrorInfo(message));

    // ------------------------------------------------------------------ request

    /// <summary>Builds the Speedle+ <c>JsonContext</c>; throws <see cref="InvalidRequestException"/>.</summary>
    internal static byte[] EncodeRequest(
        Subject subject, string serviceName, string resource, string action, IReadOnlyList<SpeedleAttribute>? attributes)
    {
        if (subject is null)
        {
            throw Invalid("subject must not be null");
        }

        if (resource is null)
        {
            throw Invalid("resource must not be null");
        }

        if (action is null)
        {
            throw Invalid("action must not be null");
        }

        var names = new HashSet<string>(StringComparer.Ordinal);
        if (attributes is not null)
        {
            foreach (SpeedleAttribute a in attributes)
            {
                if (a is null)
                {
                    throw Invalid("attributes must not contain null");
                }

                if (!names.Add(a.Name))
                {
                    throw Invalid("duplicate attribute name");
                }
            }
        }

        var buffer = new ArrayBufferWriter<byte>(256);
        using (var w = new Utf8JsonWriter(buffer))
        {
            w.WriteStartObject();
            w.WriteStartObject("subject");
            w.WriteStartArray("principals");
            foreach (Principal p in subject.Principals)
            {
                if (string.IsNullOrEmpty(p.Name))
                {
                    throw Invalid("principal name must not be empty");
                }

                w.WriteStartObject();
                w.WriteString("type", string.IsNullOrEmpty(p.Type) ? PrincipalTypes.User : p.Type);
                w.WriteString("name", p.Name);
                if (!string.IsNullOrEmpty(p.Idd))
                {
                    w.WriteString("idd", p.Idd);
                }

                w.WriteEndObject();
            }

            w.WriteEndArray();
            w.WriteEndObject();
            w.WriteString("serviceName", serviceName);
            w.WriteString("resource", resource);
            w.WriteString("action", action);
            if (attributes is { Count: > 0 })
            {
                w.WriteStartArray("attributes");
                foreach (SpeedleAttribute a in attributes)
                {
                    w.WriteStartObject();
                    w.WriteString("name", a.Name);
                    w.WriteString("type", TypeName(a.Type));
                    w.WritePropertyName("value");
                    if (a.IsArray)
                    {
                        w.WriteStartArray();
                        foreach (object v in (IEnumerable)a.Value)
                        {
                            WriteValue(w, v);
                        }

                        w.WriteEndArray();
                    }
                    else
                    {
                        WriteValue(w, a.Value);
                    }

                    w.WriteEndObject();
                }

                w.WriteEndArray();
            }

            w.WriteEndObject();
        }

        return buffer.WrittenSpan.ToArray();
    }

    private static void WriteValue(Utf8JsonWriter w, object v)
    {
        switch (v)
        {
            case string s:
                w.WriteStringValue(s);
                break;
            case double d:
                w.WriteNumberValue(d);
                break;
            case bool b:
                w.WriteBooleanValue(b);
                break;
            case DateTimeOffset t:
                w.WriteStringValue(FormatDatetime(t));
                break;
            default:
                throw new InvalidOperationException("unexpected attribute value type");
        }
    }

    /// <summary>RFC 3339 in UTC; fractional seconds only when not zero.</summary>
    internal static string FormatDatetime(DateTimeOffset t)
    {
        DateTime u = t.UtcDateTime;
        string s = u.ToString("yyyy-MM-dd'T'HH:mm:ss", CultureInfo.InvariantCulture);
        long fraction = u.Ticks % TimeSpan.TicksPerSecond;
        if (fraction != 0)
        {
            s += "." + fraction.ToString("D7", CultureInfo.InvariantCulture).TrimEnd('0');
        }

        return s + "Z";
    }

    // ------------------------------------------------------------------ inferred form

    /// <summary>Builds typed attributes from native values, sorted by name in code point order.</summary>
    internal static IReadOnlyList<SpeedleAttribute> Infer(IReadOnlyDictionary<string, object?>? map)
    {
        if (map is null || map.Count == 0)
        {
            return Array.Empty<SpeedleAttribute>();
        }

        var names = map.Keys.ToList();
        foreach (string k in names)
        {
            if (string.IsNullOrEmpty(k))
            {
                throw Invalid("attribute name must not be empty");
            }
        }

        names.Sort(CompareCodePoints);
        return names.Select(n => InferOne(n, map[n])).ToArray();
    }

    private static SpeedleAttribute InferOne(string name, object? v)
    {
        switch (v)
        {
            case string s:
                return SpeedleAttribute.String(name, s);
            case bool b:
                return SpeedleAttribute.Bool(name, b);
            case DateTimeOffset t:
                return SpeedleAttribute.Datetime(name, t);
            case DateTime:
                throw Invalid("DateTime is not accepted because its time zone can be unspecified; use DateTimeOffset");
        }

        if (AsNumber(v) is double d)
        {
            return SpeedleAttribute.Numeric(name, d);
        }

        if (v is IEnumerable items)
        {
            var list = items.Cast<object?>().ToList();
            AttributeType? type = null;
            foreach (object? item in list)
            {
                AttributeType? t = ScalarType(item);
                if (t is null || (type is not null && type != t))
                {
                    throw Invalid("an inferred list must hold values of one supported type");
                }

                type = t;
            }

            switch (type)
            {
                case AttributeType.String:
                    return SpeedleAttribute.Strings(name, list.Cast<string>());
                case AttributeType.Bool:
                    return SpeedleAttribute.Bools(name, list.Cast<bool>());
                case AttributeType.Numeric:
                    return SpeedleAttribute.Numerics(name, list.Select(x => AsNumber(x)!.Value));
                case AttributeType.Datetime:
                    return SpeedleAttribute.Datetimes(name, list.Cast<DateTimeOffset>());
            }
        }

        throw Invalid(
            "an inferred attribute must be a string, number, bool, DateTimeOffset or a non-empty list of one of these; use the typed form otherwise");
    }

    private static AttributeType? ScalarType(object? v) => v switch
    {
        string => AttributeType.String,
        bool => AttributeType.Bool,
        DateTimeOffset => AttributeType.Datetime,
        _ => AsNumber(v) is null ? null : AttributeType.Numeric,
    };

    private static double? AsNumber(object? v) => v switch
    {
        sbyte x => x,
        byte x => x,
        short x => x,
        ushort x => x,
        int x => x,
        uint x => x,
        long x => x,
        ulong x => x,
        float x => x,
        double x => x,
        decimal x => (double)x,
        _ => null,
    };

    internal static int CompareCodePoints(string a, string b)
    {
        int i = 0, j = 0;
        while (i < a.Length && j < b.Length)
        {
            int ca = CodePointAt(a, i);
            int cb = CodePointAt(b, j);
            if (ca != cb)
            {
                return ca.CompareTo(cb);
            }

            i += ca > 0xFFFF ? 2 : 1;
            j += cb > 0xFFFF ? 2 : 1;
        }

        return (a.Length - i).CompareTo(b.Length - j);
    }

    private static int CodePointAt(string s, int index) =>
        char.IsSurrogatePair(s, index) ? char.ConvertToUtf32(s, index) : s[index];

    // ------------------------------------------------------------------ response

    /// <summary>Parses an HTTP 200 body; null when it is not a valid decision.</summary>
    internal static Decision? DecodeDecision(byte[] body, long? quota)
    {
        DecisionBody? d;
        try
        {
            d = JsonSerializer.Deserialize(body, WireJsonContext.Default.DecisionBody);
        }
        catch (JsonException)
        {
            return null;
        }

        if (d?.Allowed is not bool allowed || d.Reason is not int reason)
        {
            return null;
        }

        string? message = d.ErrorMessage is { ValueKind: JsonValueKind.String } m ? m.GetString() : null;
        return new Decision(allowed, (Reason)reason, message, quota);
    }

    /// <summary>The <c>error</c> string of an error body, or null.</summary>
    internal static string? ErrorField(byte[] body)
    {
        try
        {
            ErrorBody? e = JsonSerializer.Deserialize(body, WireJsonContext.Default.ErrorBody);
            return e?.Error is { ValueKind: JsonValueKind.String } s ? s.GetString() : null;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    /// <summary>A non-negative integer, or null.</summary>
    internal static long? ParseQuota(string? header)
    {
        string? h = header?.Trim();
        return IsDigits(h) && long.TryParse(h, NumberStyles.None, CultureInfo.InvariantCulture, out long v) ? v : null;
    }

    /// <summary>A positive integer number of seconds, or null.</summary>
    internal static TimeSpan? ParseRetryAfter(string? header)
    {
        string? h = header?.Trim();
        return IsDigits(h) && long.TryParse(h, NumberStyles.None, CultureInfo.InvariantCulture, out long v) && v > 0 && v < 100_000_000
            ? TimeSpan.FromSeconds(v)
            : null;
    }

    private static bool IsDigits(string? s) => !string.IsNullOrEmpty(s) && s.Length <= 18 && s.All(c => c is >= '0' and <= '9');

    /// <summary>Shortens server text and replaces control characters before it goes into a message.</summary>
    internal static string Sanitize(string s)
    {
        var b = new StringBuilder(Math.Min(s.Length, MaxMessage) + 3);
        foreach (char c in s.Take(MaxMessage))
        {
            b.Append(char.IsControl(c) ? ' ' : c);
        }

        if (s.Length > MaxMessage)
        {
            b.Append("...");
        }

        return b.ToString();
    }
}

internal sealed class DecisionBody
{
    [JsonPropertyName("allowed")]
    public bool? Allowed { get; set; }

    [JsonPropertyName("reason")]
    public int? Reason { get; set; }

    [JsonPropertyName("errorMessage")]
    public JsonElement? ErrorMessage { get; set; }
}

internal sealed class ErrorBody
{
    [JsonPropertyName("error")]
    public JsonElement? Error { get; set; }
}

[JsonSerializable(typeof(DecisionBody))]
[JsonSerializable(typeof(ErrorBody))]
internal sealed partial class WireJsonContext : JsonSerializerContext
{
}
