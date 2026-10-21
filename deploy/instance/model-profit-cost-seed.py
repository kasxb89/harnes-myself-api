#!/usr/bin/env python3
"""Prepare auditable CNY cost rules; never contact or change a running instance.

These are supplier price estimates, not account invoices. Review channel
identities, effective dates and account discounts before saving via the root
cost-rules endpoint. Existing cost rules must be retained when appending.
"""

import argparse
from datetime import datetime
from decimal import Decimal
import importlib.util
import json
from pathlib import Path
from zoneinfo import ZoneInfo


def build_rules(blackrx_channel, deepseek_channels, toapis_channel):
    supplier_path = Path(__file__).with_name("blackrxai-video-pricing.py")
    spec = importlib.util.spec_from_file_location("blackrx_supplier_prices", supplier_path)
    supplier = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(supplier)
    shanghai = ZoneInfo("Asia/Shanghai")
    blackrx_from = int(datetime(2026, 10, 21, tzinfo=shanghai).timestamp())
    deepseek_from = int(datetime(2026, 10, 20, tzinfo=shanghai).timestamp())
    # Server Search/Fetch become separately chargeable on 1 November.
    # Until their usage is accounted for, stop this token-only estimate then.
    deepseek_until = int(datetime(2026, 11, 1, tzinfo=shanghai).timestamp())
    rules = []
    for model, _, _, _, credits, basis in supplier.MODELS:
        rules.append({
            "channel_id": blackrx_channel,
            "model_name": model,
            "effective_from": blackrx_from,
            "kind": "second" if basis == "per_second" else "request",
            "unit_cost_cny": float(Decimal(credits) / supplier.CREDITS_PER_CNY),
            "source": "https://hi.blackrxai.com/app/pricing ; verified 2026-10-21; 10 credits/CNY; supplier tariff estimate",
            # The BT failure was independently reconciled against this
            # supplier's balance. Do not infer refunds for untested routes.
            "failure_refunded": model == "bt-seedance-2.0",
        })
    for channel in deepseek_channels:
        for model in ["deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp", "deepseek-v4-pro"]:
            pro = model == "deepseek-v4-pro"
            rules.append({
                "channel_id": channel,
                "model_name": model,
                "effective_from": deepseek_from,
                "effective_until": deepseek_until,
                "kind": "token",
                "input_cost_per_million_cny": 4.5 if pro else 1,
                "cache_cost_per_million_cny": 0.15 if pro else 0.02,
                "output_cost_per_million_cny": 13.5 if pro else 4,
                "deepseek_peak_multiplier": 2,
                "source": "https://api-docs.deepseek.com/zh-cn/quick_start/pricing/ ; verified 2026-10-20 and 2026-10-21; official direct channel only; tariff estimate",
                "failure_refunded": False,
            })
    if toapis_channel is not None:
        rules.append({
            "channel_id": toapis_channel,
            "model_name": "yunshu-internal-gpt-image-2",
            "effective_from": blackrx_from,
            "kind": "image",
            "unit_cost_cny": 0.105,
            "source": "https://toapis.cn/pricing ; public gpt-image-2 cny_model_price=0.105; checked 2026-10-21; same-day reference-price backfill, account discounts not verified",
            "failure_refunded": False,
        })
    return {"schema_version": 1, "revision": 0, "rules": rules}


def positive_channel(value):
    channel = int(value)
    if not 1 <= channel <= 1000000000:
        raise argparse.ArgumentTypeError("channel ID is out of range")
    return channel


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--blackrx-channel", type=positive_channel, required=True)
    parser.add_argument("--deepseek-channel", type=positive_channel, action="append", required=True)
    parser.add_argument("--toapis-reference-channel", type=positive_channel)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    channels = [args.blackrx_channel, *args.deepseek_channel]
    if args.toapis_reference_channel is not None:
        channels.append(args.toapis_reference_channel)
    if len(set(channels)) != len(channels):
        parser.error("supplier channel IDs must be distinct")
    result = build_rules(args.blackrx_channel, args.deepseek_channel, args.toapis_reference_channel)
    encoded = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
    if args.output is not None:
        args.output.write_text(encoded)
    else:
        print(encoded, end="")


if __name__ == "__main__":
    main()
