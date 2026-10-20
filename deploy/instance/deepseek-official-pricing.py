#!/usr/bin/env python3
"""Prepare the reviewed DeepSeek CNY pricing draft; never writes production.

Official CNY and USD price tables are independent. This instance displays CNY,
so convert the official CNY rates using its current USDExchangeRate rather than
substituting the differently priced official USD table.

With --snapshot, preserve each model's configured fields and optimistic version
from GET /api/option/model_pricing. Apply through the existing administrator UI
or PATCH endpoint only after the host supports deepseek_peak().
"""

import argparse
from copy import deepcopy
from decimal import Decimal, InvalidOperation
import json
from pathlib import Path


SOURCE = "https://api-docs.deepseek.com/zh-cn/quick_start/pricing/"
MODELS = {
    "deepseek-flash": "flash",
    "deepseek-v4-flash": "flash",
    "deepseek-v4-flash-0731": "flash",
    "deepseek-v4-flash-vision-exp": "flash",
    "deepseek-v4-pro": "pro",
}
# CNY / million tokens, in cache-miss input, cache-hit input, output order.
OFF_PEAK = {"flash": ("1", "0.02", "4"), "pro": ("4.5", "0.15", "13.5")}


def expression(family, exchange_rate):
    def tier(label, multiplier):
        terms = []
        for variable, price in zip(("p", "cr", "c"), OFF_PEAK[family]):
            usd = Decimal(price) * multiplier / exchange_rate
            terms.append(f"{variable} * {format(float(usd), '.17g')}")
        return f'tier("{label}", {" + ".join(terms)})'

    return f"deepseek_peak() ? {tier('peak', 2)} : {tier('off_peak', 1)}"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--usd-exchange-rate", required=True)
    parser.add_argument("--snapshot", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    try:
        exchange_rate = Decimal(args.usd_exchange_rate)
        if not exchange_rate.is_finite() or not Decimal("0.01") <= exchange_rate <= Decimal("1000"):
            raise ValueError()
    except (InvalidOperation, ValueError):
        parser.error("USD exchange rate must be finite and between 0.01 and 1000")
    templates = {}
    for model, family in MODELS.items():
        templates[model] = {
            "billing_setting.billing_mode": "tiered_expr",
            "billing_setting.billing_expr": expression(family, exchange_rate),
        }
    result = {
        "source": SOURCE,
        "verified_on": "2026-10-20",
        "currency": "CNY",
        "usd_exchange_rate": str(exchange_rate),
        "timezone": "Asia/Shanghai",
        "peak": "Monday-Friday 09:00-12:00, 14:00-18:00, excluding Chinese public holidays",
        "off_peak_cny_per_million": OFF_PEAK,
        "peak_multiplier": 2,
        "pricing_templates": templates,
        "writes_production": False,
    }
    if args.snapshot:
        snapshot = json.loads(args.snapshot.read_text())
        entries = snapshot.get("data", snapshot)["entries"]
        changes = []
        for entry in entries:
            model = entry["model_name"]
            if model not in templates:
                continue
            if not entry.get("version") or not isinstance(entry.get("configured"), dict):
                parser.error("snapshot entries require version and configured fields")
            pricing = deepcopy(entry["configured"])
            pricing.update(templates[model])
            changes.append({"model_name": model, "expected_version": entry["version"], "pricing": pricing})
        if not changes:
            parser.error("snapshot does not contain any supported DeepSeek model")
        result["request"] = {"changes": changes}
    encoded = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
    if args.output:
        args.output.write_text(encoded)
    else:
        print(encoded, end="")


if __name__ == "__main__":
    main()
