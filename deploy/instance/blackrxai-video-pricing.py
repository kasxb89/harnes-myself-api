#!/usr/bin/env python3
"""Prepare Blackrx video prices in CNY; never contact or update production.

The supplier charges ten credits per CNY. Each customer quote adds CNY 0.10
per requested second to that supplier cost. Task expressions return USD, so
use the instance's current USDExchangeRate when preparing its pricing draft.
"""

import argparse
from decimal import Decimal, InvalidOperation, localcontext
import json
from pathlib import Path


PLUGIN_KEY = "blackrxai-video"
CREDITS_PER_CNY = Decimal("10")
MARGIN_CNY_PER_SECOND = Decimal("0.1")
SOURCE = "https://hi.blackrxai.com/app/pricing"
MODELS_SOURCE = "https://hi.blackrxai.com/v1/models"
VERIFIED_ON = "2026-10-21"

# model, supplier label, allowed seconds, default seconds, credits, billing basis
# Fixed-duration prices are per video, including both 10s and 15s for bt.
MODELS = (
    ("lec-mj-seedance-2-5-cd-720p", "Seedance 2.5 /900/720", (30,), 30, "55", "per_video"),
    ("sd-25-900-2", "Seedance 2.5 /900/720", (30,), 30, "65", "per_video"),
    ("k2-sd2.5-720p", "Seedance 2.5 满参720p", (30,), 30, "86", "per_video"),
    ("lec-sz-sd2-full-480p", "Seedance 2.5 官渠480p", (30,), 30, "85", "per_video"),
    ("video-sd25-480p", "Seedance 2.5 官渠480p", tuple(range(15, 31)), 30, "6", "per_second"),
    ("model_5", "Seedance 2.5 满血480p", tuple(range(15, 31)), 30, "4.3", "per_second"),
    ("sd2.5-720p-x2", "Seedance 2.5 满血720p", tuple(range(15, 31)), 30, "5.2", "per_second"),
    ("sd2-th-2.0-720p", "Seedance 2.0 fast /903/720", (15,), 15, "24", "per_video"),
    ("bt-seedance-2.0", "Seedance 2.0 卡人脸720p", (10, 15), 15, "15", "per_video"),
    ("lec-rs-seedance-2-0-fast", "Seedance 2.0 fast /900/720", (15,), 15, "25", "per_video"),
    ("sd20-933-720p", "Seedance 2.0 fast /933/720", (15,), 15, "32", "per_video"),
    ("sd2-hn2-480-pro", "Seedance 2.0 满血 /933/480", (15,), 15, "43", "per_video"),
    ("v3-sd2.0-933-720p", "Seedance 2.0 满血 /933/720", (15,), 15, "45", "per_video"),
    ("sd-20-9/3/3-1", "Seedance 2.0 满血 /903/720", (15,), 15, "50", "per_video"),
    ("sd-20-903", "Seedance 2.0 fast /903/480", (15,), 15, "18", "per_video"),
)


def decimal_string(value):
    """Use decimal literals without exponents or insignificant trailing zeros."""
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


def build_plan(usd_exchange_rate):
    rate = exchange_rate_value(usd_exchange_rate)
    pricing = {}
    templates = {}
    provider_expressions = {}
    # Preserve more precision than the host's float64 engine needs. All CNY
    # quotes remain exact Decimal arithmetic; USD literals round only here.
    with localcontext() as context:
        context.prec = 50
        for model, label, durations, default_seconds, credits, basis in MODELS:
            supplier_credits = Decimal(credits)
            supplier_cny = supplier_credits / CREDITS_PER_CNY
            if basis == "per_second":
                unit_usd = (supplier_cny + MARGIN_CNY_PER_SECOND) / rate
                body = f'u("seconds") * {decimal_string(unit_usd)}'
            else:
                constant_usd = supplier_cny / rate
                margin_usd = MARGIN_CNY_PER_SECOND / rate
                body = f'{decimal_string(constant_usd)} + u("seconds") * {decimal_string(margin_usd)}'
            expression = f'tier("base", {body})'
            templates[model] = {
                "billing_setting.billing_mode": "tiered_expr",
                "billing_setting.billing_expr": expression,
            }
            provider_expressions[f"{PLUGIN_KEY}::{model}"] = expression
            quotes = []
            for seconds in durations:
                cost_credits = supplier_credits * seconds if basis == "per_second" else supplier_credits
                cost_cny = cost_credits / CREDITS_PER_CNY
                margin_cny = MARGIN_CNY_PER_SECOND * seconds
                sale_cny = cost_cny + margin_cny
                quotes.append({
                    "seconds": seconds,
                    "upstream_credits": decimal_string(cost_credits),
                    "cost_cny": decimal_string(cost_cny),
                    "sale_cny": decimal_string(sale_cny),
                    "margin_cny": decimal_string(margin_cny),
                    "sale_usd": decimal_string(sale_cny / rate),
                })
            pricing[model] = {
                "supplier_label": label,
                "billing_basis": basis,
                "allowed_seconds": list(durations),
                "default_seconds": default_seconds,
                "supplier_credit_price": credits,
                "quotes": quotes,
            }
    return {
        "source": SOURCE,
        "models_source": MODELS_SOURCE,
        "verified_on": VERIFIED_ON,
        "currency": "CNY",
        "usd_exchange_rate": decimal_string(rate),
        "plugin_key": PLUGIN_KEY,
        "supplier_credits_per_cny": decimal_string(CREDITS_PER_CNY),
        "margin_cny_per_second": decimal_string(MARGIN_CNY_PER_SECOND),
        "assumed_group_ratio": "1",
        "model_count": len(pricing),
        "quote_count": sum(len(entry["quotes"]) for entry in pricing.values()),
        "pricing": pricing,
        "pricing_templates": templates,
        "provider_billing_expr": provider_expressions,
        "writes_production": False,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--usd-exchange-rate", required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    try:
        result = build_plan(args.usd_exchange_rate)
    except ValueError as error:
        parser.error(str(error))
    encoded = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
    if args.output:
        args.output.write_text(encoded, encoding="utf-8")
    else:
        print(encoded, end="")


if __name__ == "__main__":
    main()
