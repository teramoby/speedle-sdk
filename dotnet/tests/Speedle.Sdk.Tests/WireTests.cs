// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using System.Text.Json;
using Xunit;

namespace Speedle.Sdk.Tests;

public class WireTests
{
    private static string Body(Subject s, IReadOnlyList<SpeedleAttribute>? attrs) =>
        Encoding.UTF8.GetString(Wire.EncodeRequest(s, "books", "/books/1", "get", attrs));

    [Fact]
    public void EncodesFieldsInDesignOrder()
    {
        string b = Body(
            Subject.Of(Principal.User("alice"), new Principal("eng", "group", "corp")),
            new[] { SpeedleAttribute.String("team", "eng") });
        Assert.Equal(
            "{\"subject\":{\"principals\":[{\"type\":\"user\",\"name\":\"alice\"},"
            + "{\"type\":\"group\",\"name\":\"eng\",\"idd\":\"corp\"}]},"
            + "\"serviceName\":\"books\",\"resource\":\"/books/1\",\"action\":\"get\","
            + "\"attributes\":[{\"name\":\"team\",\"type\":\"string\",\"value\":\"eng\"}]}",
            b);
    }

    [Fact]
    public void OmitsAttributesWhenNone()
    {
        Assert.DoesNotContain("attributes", Body("a", Array.Empty<SpeedleAttribute>()), StringComparison.Ordinal);
        Assert.DoesNotContain("attributes", Body("a", null), StringComparison.Ordinal);
    }

    [Fact]
    public void PrincipalDefaultsAndValidation()
    {
        var p = new Principal("bob", "", "");
        Assert.Equal("user", p.Type);
        Assert.Null(p.Idd);
        Assert.Throws<InvalidRequestException>(() => new Principal(""));
        Assert.Throws<InvalidRequestException>(() => Subject.Of());
        Assert.Throws<InvalidRequestException>(() => Subject.Of(null!));
        Subject s = "carol";
        Assert.Equal("carol", Assert.Single(s.Principals).Name);
    }

    [Fact]
    public void WritesEveryTypeAndForm()
    {
        string b = Body(
            "a",
            new[]
            {
                SpeedleAttribute.Numeric("n", 4),
                SpeedleAttribute.Numeric("f", 1.5),
                SpeedleAttribute.Numerics("ns", new[] { 1, 2.25, 3 }),
                SpeedleAttribute.Bool("b", true),
                SpeedleAttribute.Bools("bs", new[] { false }),
                SpeedleAttribute.Datetime("t", DateTimeOffset.Parse("2026-11-05T05:00:00+02:00", CultureInfo.InvariantCulture)),
                SpeedleAttribute.Datetime("tf", DateTimeOffset.Parse("2026-11-05T03:00:00.5Z", CultureInfo.InvariantCulture)),
                SpeedleAttribute.Datetimes("ts", Array.Empty<DateTimeOffset>()),
                SpeedleAttribute.Strings("ss", new[] { "x", "y" }),
            });
        using JsonDocument doc = JsonDocument.Parse(b);
        JsonElement[] attrs = doc.RootElement.GetProperty("attributes").EnumerateArray().ToArray();
        Assert.Equal("{\"name\":\"n\",\"type\":\"numeric\",\"value\":4}", attrs[0].GetRawText());
        Assert.Equal(1.5, attrs[1].GetProperty("value").GetDouble());
        Assert.Equal("[1,2.25,3]", attrs[2].GetProperty("value").GetRawText());
        Assert.Equal("{\"name\":\"b\",\"type\":\"bool\",\"value\":true}", attrs[3].GetRawText());
        Assert.Equal("[false]", attrs[4].GetProperty("value").GetRawText());
        Assert.Equal("2026-11-05T03:00:00Z", attrs[5].GetProperty("value").GetString());
        Assert.Equal("2026-11-05T03:00:00.5Z", attrs[6].GetProperty("value").GetString());
        Assert.Equal("[]", attrs[7].GetProperty("value").GetRawText());
        Assert.Equal("string", attrs[8].GetProperty("type").GetString());
    }

    [Fact]
    public void RejectsInvalidAttributes()
    {
        Assert.Throws<InvalidRequestException>(() => SpeedleAttribute.String("", "x"));
        Assert.Throws<InvalidRequestException>(() => SpeedleAttribute.String("a", null!));
        Assert.Throws<InvalidRequestException>(() => SpeedleAttribute.Strings("a", new[] { "x", null! }));
        Assert.Throws<InvalidRequestException>(() => SpeedleAttribute.Strings("a", null!));
        Assert.Throws<InvalidRequestException>(() => SpeedleAttribute.Numeric("a", double.NaN));
        Assert.Throws<InvalidRequestException>(() => SpeedleAttribute.Numerics("a", new[] { 1, double.PositiveInfinity }));
        var dup = new[] { SpeedleAttribute.String("a", "x"), SpeedleAttribute.Bool("a", true) };
        Assert.Throws<InvalidRequestException>(() => Body("u", dup));
        Assert.Throws<InvalidRequestException>(() => Body("u", new SpeedleAttribute[] { null! }));
        Assert.DoesNotContain("secret", SpeedleAttribute.String("team", "secret").ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public void InfersTypesAndSortsByCodePoint()
    {
        var m = new Dictionary<string, object?>
        {
            ["zeta"] = "2026-11-05T03:00:00Z", // a string stays a string
            ["b"] = true,
            ["n"] = 4,
            ["dec"] = 1.25m,
            ["t"] = DateTimeOffset.UnixEpoch,
            ["list"] = new List<object> { 1, 2.5 },
            ["\U0001F600"] = "emoji", // sorts after U+FFFD in code point order
            ["\uFFFD"] = "replacement",
        };
        IReadOnlyList<SpeedleAttribute> attrs = Wire.Infer(m);
        Assert.Equal(new[] { "b", "dec", "list", "n", "t", "zeta", "\uFFFD", "\U0001F600" }, attrs.Select(a => a.Name));
        Assert.Equal(AttributeType.Bool, attrs[0].Type);
        Assert.Equal(AttributeType.Numeric, attrs[1].Type);
        Assert.True(attrs[2].IsArray);
        Assert.Equal(new[] { 1.0, 2.5 }, (IReadOnlyList<double>)attrs[2].Value);
        Assert.Equal(AttributeType.Datetime, attrs[4].Type);
        Assert.Equal(AttributeType.String, attrs[5].Type);
    }

    [Fact]
    public void InferenceRejectsUnsupportedValues()
    {
        object?[] bad =
        {
            null, new List<object>(), new List<object> { 1, "a" }, new Dictionary<string, object> { ["k"] = "v" },
            'c', double.NaN, new List<object?> { "a", null }, DateTime.UtcNow, new object(),
        };
        foreach (object? v in bad)
        {
            var m = new Dictionary<string, object?> { ["a"] = v };
            Assert.Throws<InvalidRequestException>(() => Wire.Infer(m));
        }

        Assert.Throws<InvalidRequestException>(() => Wire.Infer(new Dictionary<string, object?> { [""] = "x" }));
        Assert.Empty(Wire.Infer(null));
    }

    [Fact]
    public void DecodesDecisions()
    {
        Decision? d = Wire.DecodeDecision(Encoding.UTF8.GetBytes("{\"allowed\":true,\"reason\":0,\"extra\":[1]}"), 7);
        Assert.NotNull(d);
        Assert.True(d.Allowed);
        Assert.Equal(Reason.GrantPolicyFound, d.Reason);
        Assert.Equal(7L, d.QuotaDailyRemaining);
        Decision? e = Wire.DecodeDecision(Encoding.UTF8.GetBytes("{\"allowed\":false,\"reason\":4,\"errorMessage\":\"boom\"}"), null);
        Assert.Equal("boom", e?.ErrorMessage);
        Assert.Equal((Reason)42, Wire.DecodeDecision(Encoding.UTF8.GetBytes("{\"allowed\":false,\"reason\":42}"), null)?.Reason);
        string[] invalid =
        {
            "", "null", "[]", "{\"allowed\":\"true\",\"reason\":0}", "{\"allowed\":1,\"reason\":0}",
            "{\"reason\":0}", "{\"allowed\":true}", "{\"allowed\":true,\"reason\":0.5}",
            "{\"allowed\":true,\"reason\":0} x", "<html>", "{\"allowed\":null,\"reason\":0}",
        };
        foreach (string s in invalid)
        {
            Assert.Null(Wire.DecodeDecision(Encoding.UTF8.GetBytes(s), null));
        }
    }

    [Fact]
    public void ParsesHeaders()
    {
        Assert.Equal(TimeSpan.FromSeconds(60), Wire.ParseRetryAfter("60"));
        Assert.Null(Wire.ParseRetryAfter("0"));
        Assert.Null(Wire.ParseRetryAfter("-1"));
        Assert.Null(Wire.ParseRetryAfter("1.5"));
        Assert.Null(Wire.ParseRetryAfter("Wed, 21 Oct 2026 07:28:00 GMT"));
        Assert.Null(Wire.ParseRetryAfter(null));
        Assert.Equal(0L, Wire.ParseQuota("0"));
        Assert.Null(Wire.ParseQuota("-3"));
        Assert.Null(Wire.ParseQuota("abc"));
    }
}
