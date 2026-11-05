from __future__ import annotations

import datetime as dt
import math
from typing import Any

import pytest

from speedle import Attribute, InvalidRequestError
from speedle._attributes import format_datetime, to_wire_attributes

UTC = dt.timezone.utc


def test_typed_factories_map_one_to_one_in_order() -> None:
    wire = to_wire_attributes(
        [
            Attribute.string("s", "eng"),
            Attribute.string("sa", ["a", "b"]),
            Attribute.numeric("n", 4.5),
            Attribute.numeric("ni", 4),
            Attribute.numeric("na", []),
            Attribute.boolean("b", False),
            Attribute.boolean("ba", (True,)),
            Attribute.datetime("d", dt.datetime(2026, 11, 5, 3, tzinfo=UTC)),
            Attribute.datetime("da", [dt.datetime(2026, 11, 5, 3, 0, 0, 250000, tzinfo=UTC)]),
        ]
    )
    assert wire == [
        {"name": "s", "type": "string", "value": "eng"},
        {"name": "sa", "type": "string", "value": ["a", "b"]},
        {"name": "n", "type": "numeric", "value": 4.5},
        {"name": "ni", "type": "numeric", "value": 4},
        {"name": "na", "type": "numeric", "value": []},
        {"name": "b", "type": "bool", "value": False},
        {"name": "ba", "type": "bool", "value": [True]},
        {"name": "d", "type": "datetime", "value": "2026-11-05T03:00:00Z"},
        {"name": "da", "type": "datetime", "value": ["2026-11-05T03:00:00.25Z"]},
    ]


def test_attribute_fields() -> None:
    a = Attribute.string("x", ["a"])
    assert (a.name, a.type, a.value, a.is_array) == ("x", "string", ("a",), True)
    assert Attribute.boolean("y", True).type == "bool"


def test_datetime_is_converted_to_utc() -> None:
    plus2 = dt.timezone(dt.timedelta(hours=2))
    assert format_datetime(dt.datetime(2026, 11, 5, 5, 0, 0, 1000, tzinfo=plus2)) == "2026-11-05T03:00:00.001Z"


def test_inferred_form_sorted_by_code_point() -> None:
    wire = to_wire_attributes(
        {
            "z": "2026-11-05T03:00:00Z",
            "\U0001f600": 1,
            "\uffff": True,
            "a": [1, 2.5],
            "d": dt.datetime(2026, 11, 5, 3, tzinfo=UTC),
            "l": ("x", "y"),
            "t": [True, False],
        }
    )
    assert [(w["name"], w["type"]) for w in wire] == [
        ("a", "numeric"),
        ("d", "datetime"),
        ("l", "string"),
        ("t", "bool"),
        ("z", "string"),
        ("\uffff", "bool"),
        ("\U0001f600", "numeric"),
    ]
    assert wire[4]["value"] == "2026-11-05T03:00:00Z"


def test_no_attributes() -> None:
    assert to_wire_attributes(None) == []
    assert to_wire_attributes({}) == []
    assert to_wire_attributes([]) == []


REJECTED: list[tuple[str, Any]] = [
    ("empty name", [Attribute.string("", "x")]),
    ("duplicate", [Attribute.string("a", "x"), Attribute.numeric("a", 1)]),
    ("None value", [Attribute.string("a", None)]),  # type: ignore[arg-type]
    ("None item", [Attribute.string("a", ["x", None])]),  # type: ignore[list-item]
    ("NaN", [Attribute.numeric("a", math.nan)]),
    ("infinity item", [Attribute.numeric("a", [1, math.inf])]),
    ("bool as numeric", [Attribute.numeric("a", True)]),
    ("naive datetime", [Attribute.datetime("a", dt.datetime(2026, 11, 5))]),
    ("str as datetime", [Attribute.datetime("a", "2026-11-05T03:00:00Z")]),  # type: ignore[arg-type]
    ("not an Attribute", [{"name": "a", "type": "string", "value": "x"}]),
    ("inferred None", {"a": None}),
    ("inferred empty list", {"a": []}),
    ("inferred mixed list", {"a": [1, "x"]}),
    ("inferred bool and int list", {"a": [True, 1]}),
    ("inferred nested map", {"a": {"b": 1}}),
    ("inferred bytes", {"a": b"x"}),
    ("inferred naive datetime", {"a": dt.datetime(2026, 11, 5)}),
    ("inferred NaN", {"a": math.nan}),
    ("a str", "abc"),
]


@pytest.mark.parametrize(("case", "attributes"), REJECTED, ids=[c[0] for c in REJECTED])
def test_rejected(case: str, attributes: Any) -> None:
    with pytest.raises(InvalidRequestError):
        to_wire_attributes(attributes)
