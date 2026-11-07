"""Typed Speedle attributes (design §3.2.1)."""

from __future__ import annotations

import datetime as _dt
import math
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import Literal, Union

from ._errors import InvalidRequestError

__all__ = ["Attribute", "AttributeType", "Attributes", "InferredValue", "to_wire_attributes"]

AttributeType = Literal["string", "numeric", "bool", "datetime"]

_Scalar = Union[str, float, bool, _dt.datetime]


@dataclass(frozen=True)
class Attribute:
    """One Speedle ``JsonAttribute``; build it with the factories.

    Values are checked when the attribute is sent, so that a bad value is
    handled like every other request error (including fail-closed mode).
    """

    name: str
    type: AttributeType
    value: _Scalar | tuple[_Scalar, ...]
    is_array: bool

    @staticmethod
    def string(name: str, value: str | Sequence[str]) -> Attribute:
        return _make(name, "string", value)

    @staticmethod
    def numeric(name: str, value: int | float | Sequence[int | float]) -> Attribute:
        """A number or a list of numbers; ``bool`` is rejected."""
        return _make(name, "numeric", value)

    @staticmethod
    def boolean(name: str, value: bool | Sequence[bool]) -> Attribute:
        """Type ``bool``. Named ``boolean`` so as not to shadow the built-in."""
        return _make(name, "bool", value)

    @staticmethod
    def datetime(name: str, value: _dt.datetime | Sequence[_dt.datetime]) -> Attribute:
        """A time-zone-aware datetime or a list of them."""
        return _make(name, "datetime", value)


def _make(name: str, type_: AttributeType, value: object) -> Attribute:
    if isinstance(value, (list, tuple)):
        return Attribute(name, type_, tuple(value), True)
    return Attribute(name, type_, value, False)  # type: ignore[arg-type]


InferredValue = Union[
    str,
    int,
    float,
    bool,
    _dt.datetime,
    Sequence[str],
    Sequence[Union[int, float]],
    Sequence[bool],
    Sequence[_dt.datetime],
]
Attributes = Union[Sequence[Attribute], Mapping[str, InferredValue]]

WireValue = Union[str, float, bool, list[str], list[float], list[bool]]


def format_datetime(value: _dt.datetime) -> str:
    """RFC 3339 in UTC, with fractional seconds only when they are not zero."""
    utc = value.astimezone(_dt.timezone.utc)
    text = utc.strftime("%Y-%m-%dT%H:%M:%S")
    if utc.microsecond:
        text += f".{utc.microsecond:06d}".rstrip("0")
    return text + "Z"


def _invalid(message: str) -> InvalidRequestError:
    return InvalidRequestError(message)


def _convert_item(name: str, type_: AttributeType, item: object) -> str | float | bool:
    if item is None:
        raise _invalid(f"attribute {name!r}: None value")
    if type_ == "string":
        if not isinstance(item, str):
            raise _invalid(f"attribute {name!r}: expected a str")
        return item
    if type_ == "numeric":
        if isinstance(item, bool) or not isinstance(item, (int, float)):
            raise _invalid(f"attribute {name!r}: expected an int or float")
        if isinstance(item, float) and not math.isfinite(item):
            raise _invalid(f"attribute {name!r}: number is not finite")
        return item
    if type_ == "bool":
        if not isinstance(item, bool):
            raise _invalid(f"attribute {name!r}: expected a bool")
        return item
    if not isinstance(item, _dt.datetime):
        raise _invalid(f"attribute {name!r}: expected a datetime.datetime")
    if item.tzinfo is None or item.utcoffset() is None:
        raise _invalid(f"attribute {name!r}: datetime has no time zone")
    return format_datetime(item)


def _wire(name: str, type_: AttributeType, value: object, is_array: bool) -> dict[str, object]:
    converted: object
    if is_array:
        if not isinstance(value, (list, tuple)):
            raise _invalid(f"attribute {name!r}: expected a sequence")
        converted = [_convert_item(name, type_, item) for item in value]
    else:
        converted = _convert_item(name, type_, value)
    return {"name": name, "type": type_, "value": converted}


def _infer(value: object) -> AttributeType | None:
    # bool before int: True is a bool, not a number.
    if isinstance(value, bool):
        return "bool"
    if isinstance(value, str):
        return "string"
    if isinstance(value, (int, float)):
        return "numeric"
    if isinstance(value, _dt.datetime):
        return "datetime"
    return None


def _check_name(name: object, seen: set[str]) -> str:
    if not isinstance(name, str) or name == "":
        raise _invalid("attribute with an empty name")
    if name in seen:
        raise _invalid(f"attribute {name!r} given twice")
    seen.add(name)
    return name


_TYPES = frozenset(("string", "numeric", "bool", "datetime"))


def to_wire_attributes(attributes: Attributes | None) -> list[dict[str, object]]:
    """Converts the caller's attributes to the Speedle wire array.

    Raises InvalidRequestError for every check of design §3.2.1.
    """
    if attributes is None:
        return []
    seen: set[str] = set()
    if isinstance(attributes, Mapping):
        out: list[dict[str, object]] = []
        # Python string order is Unicode code point order.
        for name in sorted(attributes):
            _check_name(name, seen)
            value = attributes[name]
            if isinstance(value, (str, bytes)) or not isinstance(value, Sequence):
                type_ = _infer(value)
                is_array = False
            else:
                if len(value) == 0:
                    raise _invalid(f"attribute {name!r}: cannot infer the type of an empty list")
                kinds = {_infer(item) for item in value}
                type_ = kinds.pop() if len(kinds) == 1 else None
                is_array = True
            if type_ is None:
                raise _invalid(f"attribute {name!r}: cannot infer a Speedle type; use Attribute")
            out.append(_wire(name, type_, value, is_array))
        return out
    if isinstance(attributes, (str, bytes)) or not isinstance(attributes, Sequence):
        raise _invalid("attributes must be a sequence of Attribute or a mapping")
    result: list[dict[str, object]] = []
    for attr in attributes:
        if not isinstance(attr, Attribute):
            raise _invalid("attributes must be Attribute values")
        name = _check_name(attr.name, seen)
        if attr.type not in _TYPES:
            raise _invalid(f"attribute {name!r}: unknown type")
        result.append(_wire(name, attr.type, attr.value, attr.is_array))
    return result
