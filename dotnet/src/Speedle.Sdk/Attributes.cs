// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Linq;

namespace Speedle.Sdk;

/// <summary>The server a client talks to. It is never auto-detected.</summary>
public enum Backend
{
    /// <summary>Speedle Cloud ADS. Requires <see cref="Auth.Bearer"/>.</summary>
    SpeedleCloud,

    /// <summary>A self-hosted Speedle+ ADS.</summary>
    SpeedlePlus,
}

/// <summary>The four Speedle+ principal types; <see cref="Principal.Type"/> is an open string.</summary>
public static class PrincipalTypes
{
    /// <summary><c>user</c>.</summary>
    public const string User = "user";

    /// <summary><c>group</c>.</summary>
    public const string Group = "group";

    /// <summary><c>role</c>.</summary>
    public const string Role = "role";

    /// <summary><c>entity</c>.</summary>
    public const string Entity = "entity";
}

/// <summary>One principal of a subject.</summary>
/// <param name="Name">The principal name; required.</param>
/// <param name="Type">The principal type; null or empty means <c>user</c>.</param>
/// <param name="Idd">The identity domain; sent only when not empty.</param>
public sealed record Principal(string Name, string Type = "user", string? Idd = null)
{
    /// <summary>The principal name.</summary>
    public string Name { get; init; } = string.IsNullOrEmpty(Name)
        ? throw new InvalidRequestException(new ErrorInfo("principal name must not be empty"))
        : Name;

    /// <summary>The principal type.</summary>
    public string Type { get; init; } = string.IsNullOrEmpty(Type) ? PrincipalTypes.User : Type;

    /// <summary>The identity domain, or null.</summary>
    public string? Idd { get; init; } = string.IsNullOrEmpty(Idd) ? null : Idd;

    /// <summary>A <c>user</c> principal.</summary>
    /// <param name="name">The user name.</param>
    /// <returns>The principal.</returns>
    public static Principal User(string name) => new(name, PrincipalTypes.User);

    /// <summary>A <c>group</c> principal.</summary>
    /// <param name="name">The group name.</param>
    /// <returns>The principal.</returns>
    public static Principal Group(string name) => new(name, PrincipalTypes.Group);
}

/// <summary>
/// The subject of a check: one or more principals. A string converts to one <c>user</c> principal.
/// Subjects must come from the application's own authenticated context.
/// </summary>
public sealed class Subject
{
    private Subject(IReadOnlyList<Principal> principals) => Principals = principals;

    /// <summary>The principals, in order.</summary>
    public IReadOnlyList<Principal> Principals { get; }

    /// <summary>A subject with one <c>user</c> principal.</summary>
    /// <param name="name">The user name.</param>
    /// <returns>The subject.</returns>
    public static Subject User(string name) => new(new[] { Principal.User(name) });

    /// <summary>A subject with the given principals.</summary>
    /// <param name="principals">At least one principal, none null.</param>
    /// <returns>The subject.</returns>
    public static Subject Of(params Principal[] principals)
    {
        if (principals is null || principals.Length == 0)
        {
            throw new InvalidRequestException(new ErrorInfo("subject needs at least one principal"));
        }

        if (principals.Any(p => p is null))
        {
            throw new InvalidRequestException(new ErrorInfo("subject principals must not be null"));
        }

        return new Subject(principals.ToArray());
    }

    /// <summary>Converts a user name to a subject with one <c>user</c> principal.</summary>
    /// <param name="userName">The user name.</param>
    public static implicit operator Subject(string userName) => User(userName);

    /// <inheritdoc/>
    public override string ToString() => $"Subject[{Principals.Count} principal(s)]";
}

/// <summary>The four Speedle+ attribute types.</summary>
public enum AttributeType
{
    /// <summary>Wire name <c>string</c>.</summary>
    String,

    /// <summary>Wire name <c>numeric</c>: a 64-bit float on the server.</summary>
    Numeric,

    /// <summary>Wire name <c>bool</c>.</summary>
    Bool,

    /// <summary>Wire name <c>datetime</c>: RFC 3339 in UTC, stored by Speedle+ as Unix seconds.</summary>
    Datetime,
}

/// <summary>
/// One Speedle+ <c>JsonAttribute</c>: a name, a type and one value or a list of values of that
/// type. Named <c>SpeedleAttribute</c> to avoid <see cref="System.Attribute"/>. Null names, values
/// or items, empty names and non-finite numbers throw <see cref="InvalidRequestException"/>.
/// Integers above 2^53 lose precision on the server.
/// </summary>
public sealed class SpeedleAttribute
{
    private SpeedleAttribute(string name, AttributeType type, bool isArray, object value)
    {
        Name = name;
        Type = type;
        IsArray = isArray;
        Value = value;
    }

    /// <summary>The attribute name.</summary>
    public string Name { get; }

    /// <summary>The attribute type.</summary>
    public AttributeType Type { get; }

    /// <summary>Whether this is the array form.</summary>
    public bool IsArray { get; }

    /// <summary>A string, double, bool or DateTimeOffset, or an IReadOnlyList of one of these.</summary>
    public object Value { get; }

    /// <summary>A <c>string</c> attribute.</summary>
    /// <param name="name">The attribute name.</param>
    /// <param name="value">The value.</param>
    /// <returns>The attribute.</returns>
    public static SpeedleAttribute String(string name, string value) =>
        Single(name, AttributeType.String, value);

    /// <summary>A <c>string</c> array attribute.</summary>
    /// <param name="name">The attribute name.</param>
    /// <param name="values">The values, possibly empty.</param>
    /// <returns>The attribute.</returns>
    public static SpeedleAttribute Strings(string name, IEnumerable<string> values) =>
        ArrayOf(name, AttributeType.String, values);

    /// <summary>A <c>numeric</c> attribute.</summary>
    /// <param name="name">The attribute name.</param>
    /// <param name="value">A finite number.</param>
    /// <returns>The attribute.</returns>
    public static SpeedleAttribute Numeric(string name, double value) =>
        Single(name, AttributeType.Numeric, Finite(value));

    /// <summary>A <c>numeric</c> array attribute.</summary>
    /// <param name="name">The attribute name.</param>
    /// <param name="values">Finite numbers, possibly empty.</param>
    /// <returns>The attribute.</returns>
    public static SpeedleAttribute Numerics(string name, IEnumerable<double> values)
    {
        RequireName(name);
        if (values is null)
        {
            throw NullValue();
        }

        return new SpeedleAttribute(name, AttributeType.Numeric, true, ReadOnly(values.Select(Finite).ToArray()));
    }

    /// <summary>A <c>bool</c> attribute.</summary>
    /// <param name="name">The attribute name.</param>
    /// <param name="value">The value.</param>
    /// <returns>The attribute.</returns>
    public static SpeedleAttribute Bool(string name, bool value) => Single(name, AttributeType.Bool, value);

    /// <summary>A <c>bool</c> array attribute.</summary>
    /// <param name="name">The attribute name.</param>
    /// <param name="values">The values, possibly empty.</param>
    /// <returns>The attribute.</returns>
    public static SpeedleAttribute Bools(string name, IEnumerable<bool> values)
    {
        RequireName(name);
        if (values is null)
        {
            throw NullValue();
        }

        return new SpeedleAttribute(name, AttributeType.Bool, true, ReadOnly(values.ToArray()));
    }

    /// <summary>A <c>datetime</c> attribute, sent as RFC 3339 in UTC.</summary>
    /// <param name="name">The attribute name.</param>
    /// <param name="value">The point in time.</param>
    /// <returns>The attribute.</returns>
    public static SpeedleAttribute Datetime(string name, DateTimeOffset value) =>
        Single(name, AttributeType.Datetime, value);

    /// <summary>A <c>datetime</c> array attribute.</summary>
    /// <param name="name">The attribute name.</param>
    /// <param name="values">The points in time, possibly empty.</param>
    /// <returns>The attribute.</returns>
    public static SpeedleAttribute Datetimes(string name, IEnumerable<DateTimeOffset> values)
    {
        RequireName(name);
        if (values is null)
        {
            throw NullValue();
        }

        return new SpeedleAttribute(name, AttributeType.Datetime, true, ReadOnly(values.ToArray()));
    }

    /// <summary>Omits the value, so attributes do not end up in logs by accident.</summary>
    /// <returns>The name and type.</returns>
    public override string ToString() => $"SpeedleAttribute[{Name}, {Wire.TypeName(Type)}{(IsArray ? "[]" : "")}]";

    private static SpeedleAttribute Single(string name, AttributeType type, object? value)
    {
        RequireName(name);
        return value is null ? throw NullValue() : new SpeedleAttribute(name, type, false, value);
    }

    private static SpeedleAttribute ArrayOf<T>(string name, AttributeType type, IEnumerable<T> values)
        where T : class
    {
        RequireName(name);
        if (values is null)
        {
            throw NullValue();
        }

        T[] items = values.ToArray();
        if (items.Any(v => v is null))
        {
            throw NullValue();
        }

        return new SpeedleAttribute(name, type, true, ReadOnly(items));
    }

    private static ReadOnlyCollection<T> ReadOnly<T>(T[] items) => new(items);

    private static void RequireName(string name)
    {
        if (string.IsNullOrEmpty(name))
        {
            throw new InvalidRequestException(new ErrorInfo("attribute name must not be empty"));
        }
    }

    private static InvalidRequestException NullValue() =>
        new(new ErrorInfo("attribute values must not be null"));

    private static double Finite(double d) =>
        double.IsFinite(d) ? d : throw new InvalidRequestException(new ErrorInfo("numeric attributes must be finite"));
}
