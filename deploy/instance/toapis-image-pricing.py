#!/usr/bin/env python3
"""Prepare reviewed ToAPIs image resolution prices; never update production.

The user-approved CNY sale quotes are per completed image. Task expressions
return USD, so pass the instance's current USDExchangeRate. With --snapshot,
preserve the exact model draft and optimistic version from model_pricing,
updating an existing ToAPIs provider override if one is already configured.
"""

import argparse
from copy import deepcopy
from decimal import Decimal, InvalidOperation, localcontext
import json
from pathlib import Path


MODEL = "yunshu-internal-gpt-image-2"
PLUGIN_KEY = "toapis-image"
PLUGIN_VERSION = "1.2.0"
PROVIDER_EXPRESSIONS_KEY = "billing_setting.plugin_billing_expr"
SOURCE = "https://github.com/toapis/docs/blob/main/docs/cn/api-reference/images/gpt-image-2/generation.mdx"
# Procurement estimates and customer prices confirmed by the user, CNY/image.
QUOTES = {"1k": ("0.1", "0.2"), "2k": ("0.15", "0.3"), "4k": ("0.2", "0.4")}


def decimal_string(value):
    rendered = format(value, "f")
    return rendered.rstrip("0").rstrip(".") if "." in rendered else rendered


def exchange_rate_value(value):
    try:
        rate = Decimal(str(value))
        if not rate.is_finite() or not Decimal("0.01") <= rate <= Decimal("1000"):
            raise ValueError()
    except (InvalidOperation, ValueError):
        raise ValueError("USD exchange rate must be finite and between 0.01 and 1000") from None
    return rate


def build_plan(usd_exchange_rate, snapshot=None):
    rate = exchange_rate_value(usd_exchange_rate)
    quotes = {}
    with localcontext() as context:
        context.prec = 50
        for resolution, (cost, sale) in QUOTES.items():
            cost, sale = Decimal(cost), Decimal(sale)
            quotes[resolution] = {
                "cost_estimate_cny_per_image": decimal_string(cost),
                "sale_cny_per_image": decimal_string(sale),
                "gross_profit_estimate_cny_per_image": decimal_string(sale - cost),
                "gross_margin_estimate": decimal_string((sale - cost) / sale),
                "sale_usd_per_image": decimal_string(sale / rate),
            }
        tiers = {key: f'tier("{key}", u("image_count") * {quote["sale_usd_per_image"]})'
                 for key, quote in quotes.items()}
    expression = (f'u("resolution") == "4k" ? {tiers["4k"]} : '
                  f'u("resolution") == "2k" ? {tiers["2k"]} : {tiers["1k"]}')
    template = {"billing_setting.billing_mode": "tiered_expr",
                "billing_setting.billing_expr": expression}
    result = {
        "source": SOURCE,
        "price_source": "user-confirmed CNY procurement estimates and retail prices, 2026-10-21",
        "verified_on": "2026-10-21",
        "currency": "CNY",
        "usd_exchange_rate": decimal_string(rate),
        "plugin_key": PLUGIN_KEY,
        "required_plugin_version": PLUGIN_VERSION,
        "model_name": MODEL,
        "default_resolution": "1k",
        "required_usage_facts": ["image_count", "resolution"],
        "assumed_group_ratio": "1",
        "quotes": quotes,
        "pricing_templates": {MODEL: template},
        "provider_billing_expr": {f"{PLUGIN_KEY}::{MODEL}": expression},
        "cost_basis": "user quote; procurement estimate, not a supplier invoice",
        "writes_production": False,
    }
    if snapshot is not None:
        if not isinstance(snapshot, dict):
            raise ValueError("snapshot must be a model_pricing JSON object")
        data = snapshot.get("data", snapshot)
        if not isinstance(data, dict) or not isinstance(data.get("entries"), list):
            raise ValueError("snapshot requires model_pricing entries")
        entries = [entry for entry in data["entries"]
                   if isinstance(entry, dict) and entry.get("model_name") == MODEL]
        if len(entries) != 1:
            raise ValueError("snapshot requires exactly one ToAPIs image model")
        entry = entries[0]
        if not entry.get("version") or not isinstance(entry.get("configured"), dict):
            raise ValueError("snapshot entry requires version and configured fields")
        pricing = deepcopy(entry["configured"])
        pricing.update(template)
        # A saved provider override wins over the model expression. Align only
        # this provider's existing override; keep unrelated providers untouched.
        if PROVIDER_EXPRESSIONS_KEY in pricing:
            variants = pricing[PROVIDER_EXPRESSIONS_KEY]
            if not isinstance(variants, dict):
                raise ValueError("snapshot provider expressions must be an object")
            if PLUGIN_KEY in variants:
                variants[PLUGIN_KEY] = expression
        result["request"] = {"changes": [{"model_name": MODEL,
                                          "expected_version": entry["version"],
                                          "pricing": pricing}]}
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--usd-exchange-rate", required=True)
    parser.add_argument("--snapshot", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    try:
        snapshot = json.loads(args.snapshot.read_text(encoding="utf-8")) if args.snapshot else None
        result = build_plan(args.usd_exchange_rate, snapshot)
    except (ValueError, OSError) as error:
        parser.error(str(error))
    encoded = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
    if args.output:
        args.output.write_text(encoded, encoding="utf-8")
    else:
        print(encoded, end="")


if __name__ == "__main__":
    main()
