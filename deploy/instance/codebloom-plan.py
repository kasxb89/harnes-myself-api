#!/usr/bin/env python3
"""Print an offline, disabled review plan. Never reads credential values or applies it.

The embedded catalog stores account-credit costs and explicitly approved retail
references. Cash procurement cost still needs the unknown credit-purchase rate.
"""

import argparse
import copy
import json
import math
from pathlib import Path


CATALOG_PATH = Path(__file__).resolve().parents[2] / "pkg/codebloom/catalog.json"
GROUPS = (
    ("CODEBLOOM_GPT_KEY", "codebloom-gpt-text", "CodeBloom GPT Text", 1, 6, 0.25),
    ("CODEBLOOM_CN_KEY", "codebloom-cn-text", "CodeBloom CN Text", 1, 21, 0.5),
    ("CODEBLOOM_IMAGE_KEY", "codebloom-image", "CodeBloom Image", 61, 10, 0.75),
)


def validate_catalog(catalog):
    """Reject missing qualifications instead of emitting active or priced routes."""
    models = catalog.get("models", [])
    if (
        catalog.get("schema_version") != 1
        or catalog.get("count") != 37
        or len(models) != 37
        or len(catalog.get("excluded", [])) != 9
        or catalog.get("generation_enabled") is not False
        or catalog.get("strict_multimodal_gate") is not True
        or catalog.get("retail_pricing_status") != "approved"
        or catalog.get("cash_procurement_status") != "credit_purchase_exchange_rate_unknown"
    ):
        raise ValueError("Invalid CodeBloom candidate review policy")
    ids = set()
    counts = {env: 0 for env, *_ in GROUPS}
    groups = {row[0]: row for row in GROUPS}
    for model in models:
        public_id = model.get("id")
        raw_id = model.get("upstream_id")
        if not raw_id or public_id != "ys-" + raw_id or model.get("name") != public_id or public_id in ids:
            raise ValueError("Invalid CodeBloom candidate identity")
        ids.add(public_id)
        env = model.get("credential_env")
        if env not in groups:
            raise ValueError("Unknown CodeBloom credential reference")
        counts[env] += 1
        if model.get("account_multiplier") != groups[env][5]:
            raise ValueError("Unverified CodeBloom account multiplier")
        if (
            model.get("activation_ready") is not False
            or model.get("cash_cost_cny") is not None
            or model.get("context_window") is not None
            or model.get("max_output_tokens") is not None
            or model.get("currency") != "CNY_ACCOUNT_CREDIT"
            or model.get("cost_basis") != "account_credit_snapshot"
            or not model.get("base_billing_expression")
            or model.get("kind") not in ("text", "image")
            or model.get("transport") not in ("chat_completions", "images_generations")
        ):
            raise ValueError("Candidate is not qualified for activation or cash costing")
        if (env == "CODEBLOOM_IMAGE_KEY") != (model["kind"] == "image"):
            raise ValueError("Candidate uses the wrong credential purpose")
        if model["kind"] == "text" and model["transport"] != "chat_completions":
            raise ValueError("Unverified text transport")
        if not {"credentials_not_configured", "multimodal_input_unverified", "complete_generation_contract_unverified"}.issubset(model.get("pending_reasons", [])):
            raise ValueError("Missing candidate qualification")
        if any(model.get("capability_status", {}).get(feature) != "unverified" for feature in ("vision_input", "tools", "reasoning_controls", "async_polling")):
            raise ValueError("Unverified capabilities cannot be enabled")
        costs = model.get("cost_components", [])
        if not costs or any(
            not isinstance(cost.get("account_credit_cny"), (int, float))
            or isinstance(cost.get("account_credit_cny"), bool)
            or not math.isfinite(cost["account_credit_cny"])
            or cost["account_credit_cny"] < 0
            for cost in costs
        ):
            raise ValueError("Invalid account-credit cost")
        if model["kind"] == "text":
            if (
                model.get("retail_cny") is not None
                or model.get("retail_pricing_status") != "approved_account_multiplier_plus_0_20"
                or model.get("retail_account_multiplier") != groups[env][5] + 0.20
                or model.get("retail_markup_cny") is not None
            ):
                raise ValueError("Unapproved text retail policy")
        elif (
            model.get("retail_cny") is None
            or model.get("retail_pricing_status") != "approved_image_cost_plus_0_10"
            or model.get("retail_markup_cny") != 0.10
            or model.get("retail_account_multiplier") is not None
            or len(costs) != 1
            or model["retail_cny"] != costs[0].get("retail_cny")
        ):
            raise ValueError("Unapproved fixed image retail policy")
        for cost in costs:
            retail = cost.get("retail_cny")
            expected = cost["account_credit_cny"] + 0.10
            if model["kind"] == "text":
                expected = cost["account_credit_cny"] / model["account_multiplier"] * model["retail_account_multiplier"]
            if not isinstance(retail, (int, float)) or isinstance(retail, bool) or not math.isfinite(retail) or not math.isclose(retail, expected, rel_tol=1e-12, abs_tol=1e-12):
                raise ValueError("Approved retail component is missing or changed")
    if any(counts[env] != count for env, _, _, _, count, _ in GROUPS):
        raise ValueError("CodeBloom candidate groups changed")
    if any("ys-" + item["id"] in ids for item in catalog["excluded"]):
        raise ValueError("Excluded candidate cannot be restored")


def build_plan(catalog=None):
    """Return review data only; neither environment variables nor a DB are read."""
    if catalog is None:
        catalog = json.loads(CATALOG_PATH.read_text(encoding="utf-8"))
    validate_catalog(catalog)
    channels = []
    abilities = []
    for env, reference, title, channel_type, _, _ in GROUPS:
        models = [model for model in catalog["models"] if model["credential_env"] == env]
        settings = {"task_plugin_key": "codebloom-image"} if channel_type == 61 else {}
        channels.append({
            "review_ref": reference,
            "name": title + " · REVIEW_DISABLED",
            "type": channel_type,
            "status": 2,
            "base_url": "https://codebloom.club",
            "group": "default",
            "models": ",".join(model["id"] for model in models),
            "model_mapping": json.dumps({model["id"]: model["upstream_id"] for model in models}, separators=(",", ":"), ensure_ascii=False),
            "setting": json.dumps(settings, separators=(",", ":")),
            "credential_env": env,
            "credentials_configured": False,
            "activation_ready": False,
        })
        abilities.extend({
            "channel_ref": reference,
            "group": "default",
            "model": model["id"],
            "enabled": False,
        } for model in models)
    costs = [{
        "model": model["id"],
        "credential_env": model["credential_env"],
        "cost_basis": "account_credit_snapshot",
        "currency": "CNY_ACCOUNT_CREDIT",
        "base_billing_expression": model["base_billing_expression"],
        "account_multiplier": model["account_multiplier"],
        "cost_components": copy.deepcopy(model["cost_components"]),
        "cost_conditions": copy.deepcopy(model["cost_conditions"]),
        "cost_components_include_account_multiplier_once": True,
        "base_expression_includes_account_multiplier": False,
        "cash_cost_cny": None,
        "retail_cny": model["retail_cny"],
        "retail_pricing_status": model["retail_pricing_status"],
        "retail_account_multiplier": model["retail_account_multiplier"],
        "retail_markup_cny": model["retail_markup_cny"],
        "retail_checked_at": model["retail_checked_at"],
        "activation_ready": False,
        "pending_reasons": list(model["pending_reasons"]),
    } for model in catalog["models"]]
    return {
        "schema_version": 1,
        "status": "REVIEW_ONLY_DISABLED_NOT_APPLIED",
        "source_version": catalog["version"],
        "source_contract_sha256": catalog["source_contract_sha256"],
        "source_capability_sha256": catalog["source_capability_sha256"],
        "checked_at": catalog["checked_at"],
        "credentials_read": False,
        "network_requests": 0,
        "database_writes": 0,
        "paid_requests": 0,
        "channels": channels,
        "abilities": abilities,
        "cost_review": costs,
        "retail_pricing_status": "approved",
        "retail_policies": copy.deepcopy(catalog["retail_policies"]),
        "retail_prices": [{
            "model": model["id"],
            "currency": "CNY",
            "policy": model["retail_pricing_status"],
            "price_components": copy.deepcopy(model["cost_components"]),
            "retail_cny_reference_expression": (
                "(" + model["base_billing_expression"] + ") * " + str(model["retail_account_multiplier"])
                if model["kind"] == "text"
                else (
                    'tier("image", fixed(' + str(model["retail_cny"]) + ')) * image_count'
                    if model["transport"] == "images_generations"
                    else 'tier("request", fixed(' + str(model["retail_cny"]) + '))'
                )
            ),
            "gateway_usd_expression_generated": False,
            "activation_ready": False,
        } for model in catalog["models"]],
        "cash_procurement_status": "credit_purchase_exchange_rate_unknown",
        "option_updates": [],
        "cost_rule_updates": [],
        "existing_model_updates": [],
        "excluded": copy.deepcopy(catalog["excluded"]),
        "notes": [
            "This document is a review schema, not an executable migration or channel-create payload.",
            "Credential environment names are references only; values are never read or printed.",
            "All channels are disabled(status2) and all abilities are false; no accepted task or retail quote can be created from this plan.",
            "Account multipliers are supplier procurement/account-credit facts, never user group ratios or retail markups.",
            "Cost components already include the account multiplier once. The original raw expression requires that multiplier once if evaluated separately; do not multiply components again.",
            "Text retail uses the explicitly approved account multiplier plus0.20 (GPT0.45,CN0.70), not procurement cost times1.20. Image retail adds0.10CNY per original billing unit with the verified single-output payload.",
            "Retail expressions above are CNY review references, not executable gateway-USD options; no runtime exchange rate or user group ratio is changed.",
            "Cash cost remains unknown. No existing prices, billing expressions, cost-rule history or options are changed.",
            "Vision/tools/reference-image/edit/upload/capacity need separate verification; old models and excluded candidates are not replaced or revived.",
        ],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--catalog", type=Path, default=CATALOG_PATH, help="Read a reviewed local candidate snapshot")
    args = parser.parse_args()
    catalog = json.loads(args.catalog.read_text(encoding="utf-8"))
    print(json.dumps(build_plan(catalog), ensure_ascii=False, indent=2, allow_nan=False))


if __name__ == "__main__":
    main()
