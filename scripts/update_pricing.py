#!/usr/bin/env python3

import json
import pathlib
import urllib.request

LITELLM_REVISION = "49ca04d8c3ddea336237ce6f3082dbc26d19e944"
LITELLM_UPDATED_AT = "2026-06-11T04:31:08Z"
LITELLM_PATH = "model_prices_and_context_window.json"
LITELLM_URL = f"https://raw.githubusercontent.com/BerriAI/litellm/{LITELLM_REVISION}/{LITELLM_PATH}"
OUTPUT = pathlib.Path(__file__).resolve().parents[1] / "internal" / "accounting" / "pricing_default.json"

MODELS = {
    "gpt-5.4": "gpt-5.4",
    "gpt-5.3-codex": "gpt-5.3-codex",
    "gpt-5.2-codex": "gpt-5.2-codex",
    "gpt-5.2": "gpt-5.2",
    "gpt-5.1-codex": "gpt-5.1-codex",
    "gpt-5.1": "gpt-5.1",
    "gpt-5": "gpt-5",
    "gpt-5-mini": "gpt-5-mini",
    "gpt-5-nano": "gpt-5-nano",
    "gpt-4.1": "gpt-4.1",
    "gpt-4.1-mini": "gpt-4.1-mini",
    "gpt-4.1-nano": "gpt-4.1-nano",
    "gpt-4o": "gpt-4o",
    "gpt-4o-mini": "gpt-4o-mini",
    "o3": "o3",
    "o4-mini": "o4-mini",
    "claude-opus-4-8": "claude-opus-4-8",
    "claude-opus-4-7": "claude-opus-4-7",
    "claude-opus-4-6": "claude-opus-4-6",
    "claude-opus-4-5": "claude-opus-4-5",
    "claude-sonnet-4-6": "claude-sonnet-4-6",
    "claude-sonnet-4-5": "claude-sonnet-4-5",
    "claude-haiku-4-5-20251001": "claude-haiku-4-5-20251001",
    "claude-haiku-4-5": "claude-haiku-4-5",
}


def per_million(value: float) -> float:
    return round(value * 1_000_000, 9)


def main() -> None:
    with urllib.request.urlopen(LITELLM_URL, timeout=30) as response:
        source = json.load(response)

    items = []
    for model, source_model in MODELS.items():
        raw = source.get(source_model)
        if raw is None:
            raise RuntimeError(f"LiteLLM pricing is missing {source_model}")
        input_cost = raw.get("input_cost_per_token")
        output_cost = raw.get("output_cost_per_token")
        if input_cost is None or output_cost is None:
            raise RuntimeError(f"LiteLLM pricing is incomplete for {source_model}")
        cache_create = raw.get("cache_creation_input_token_cost", input_cost * 1.25)
        cache_read = raw.get("cache_read_input_token_cost", input_cost * 0.1)
        items.append({
            "model": model,
            "input_per_million_usd": per_million(input_cost),
            "output_per_million_usd": per_million(output_cost),
            "cache_create_per_million_usd": per_million(cache_create),
            "cache_read_per_million_usd": per_million(cache_read),
        })

    snapshot = {
        "source": {
            "repository": "BerriAI/litellm",
            "revision": LITELLM_REVISION,
            "path": LITELLM_PATH,
            "updated_at": LITELLM_UPDATED_AT,
        },
        "items": items,
    }
    OUTPUT.write_text(json.dumps(snapshot, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
