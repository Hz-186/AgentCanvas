"""Low-risk Python tools exposed through the Bridge."""

from __future__ import annotations

import hashlib
import json
from typing import Any, Callable


Tool = Callable[[dict[str, Any]], dict[str, Any]]


def text_stats(arguments: dict[str, Any]) -> dict[str, Any]:
    text = arguments.get("text")
    if not isinstance(text, str):
        raise ValueError("text is required")
    return {
        "chars": len(text),
        "lines": text.count("\n") + 1 if text else 0,
        "words": len(text.split()),
        "sha256": hashlib.sha256(text.encode("utf-8")).hexdigest(),
    }


def json_transform(arguments: dict[str, Any]) -> dict[str, Any]:
    if "value" not in arguments:
        raise ValueError("value is required")
    value = arguments.get("value")
    if not isinstance(value, (dict, list, str, int, float, bool)) and value is not None:
        raise ValueError("value must be JSON data")
    return {"value": value, "json": json.dumps(value, ensure_ascii=False, sort_keys=True)}


TOOL_DEFINITIONS: dict[str, dict[str, Any]] = {
    "python_text_stats": {
        "description": "Compute deterministic statistics for UTF-8 text.",
        "parameters": {
            "type": "object",
            "properties": {"text": {"type": "string"}},
            "required": ["text"],
            "additionalProperties": False,
        },
        "risk_level": "low",
        "side_effect": "none",
        "version": "1",
        "handler": text_stats,
    },
    "python_json_transform": {
        "description": "Serialize JSON data deterministically without side effects.",
        "parameters": {
            "type": "object",
            "properties": {"value": {}},
            "required": ["value"],
            "additionalProperties": False,
        },
        "risk_level": "low",
        "side_effect": "none",
        "version": "1",
        "handler": json_transform,
    },
}
