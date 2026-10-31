#!/usr/bin/env python3
"""Render H3 sale/cost configuration for review. Never contacts a server.

The caller must select a fresh effective time when applying additions through
the cost-rules API. Existing cost history and other plugin prices must be kept.
H3 material cost rules require a host supporting kind=h3_video.
"""

import argparse
from decimal import Decimal
import json


def build_config(effective_from, channels, usd_rate):
    if effective_from <= 0 or effective_from > 253402300799:
        raise ValueError("Select an explicit valid effective time")
    if not channels or len(set(channels)) != len(channels) or any(not 1 <= c <= 1000000000 for c in channels):
        raise ValueError("Channel IDs must be positive and unique")
    if not usd_rate.is_finite() or not Decimal("0.01") <= usd_rate <= Decimal("1000000"):
        raise ValueError("Invalid CNY per USD conversion")
    rate = format(usd_rate, "f")
    # Preserve the existing output coefficient exactly. Material surcharges are
    # the supplier's rounded CNY reference prices, converted to gateway USD.
    tiers = []
    for resolution, input_price in [("2K", "0.15"), ("768P", "0.09")]:
        tiers.append(
            f'tier("{resolution}", u("seconds") * 0.0410959 + '
            f'u("input_video_seconds") * ({input_price} / {rate}) + '
            f'max(u("input_images") - 5, 0) * (0.05 / {rate}))'
        )
    expression = 'u("resolution") == "2K" ? ' + tiers[0] + " : " + tiers[1]
    point_cny = Decimal(4399) / Decimal(500000)
    additions = [
        {
            "channel_id": channel,
            "model_name": "MiniMax-H3",
            "effective_from": effective_from,
            "kind": "h3_video",
            "resolution_costs_cny": {
                "768P": float(Decimal("10.2") * point_cny),
                "2K": float(Decimal("17") * point_cny),
            },
            "extra_image_cost_cny": float(Decimal("5.7") * point_cny),
            "free_input_images": 5,
            "source": "https://metaso.cn/minimax-h3 ; standard <=15s reference points10.2/17 per second,5.7 per extra image;4399CNY/500000points; checked2026-10-31; estimate, not account invoice",
            "failure_refunded": False,
        }
        for channel in channels
    ]
    return {
        "schema": 1,
        "status": "REVIEW_ONLY_NOT_APPLIED",
        "model": "MiniMax-H3",
        "plugin_key": "hailuo",
        "usd_exchange_rate": float(usd_rate),
        "expected_previous_expression": 'u("seconds") * 0.0410959',
        "plugin_billing_expr": {"hailuo::MiniMax-H3": expression},
        "cost_rule_additions": additions,
        "sale_cny": {"output_per_second": float(Decimal("0.0410959") * usd_rate),
                     "reference_video_per_second": {"768P": 0.09, "2K": 0.15},
                     "extra_reference_image": 0.05, "free_reference_images": 5},
        "supported": {"resolutions": ["768P", "2K"], "min_seconds": 4, "max_seconds": 15},
    }


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--effective-from", type=int, required=True)
    parser.add_argument("--channel", type=int, action="append", required=True)
    parser.add_argument("--usd-rate", type=Decimal, default=Decimal("7.3"))
    args = parser.parse_args()
    try:
        config = build_config(args.effective_from, args.channel, args.usd_rate)
    except ValueError as error:
        parser.error(str(error))
    print(json.dumps(config, ensure_ascii=False, indent=2))
