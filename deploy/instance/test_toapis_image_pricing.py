"""Offline ToAPIs image price, currency conversion, and draft CAS checks."""

from copy import deepcopy
from decimal import Decimal, localcontext
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys
import unittest


SCRIPT = Path(__file__).with_name("toapis-image-pricing.py")
SPEC = importlib.util.spec_from_file_location("toapis_image_pricing", SCRIPT)
PRICING = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PRICING)


class ToapisImagePricingTest(unittest.TestCase):
    def test_three_user_quotes_and_actual_count_expression(self):
        for rate in ("0.01", "7.3", "7.123456789", "1000"):
            plan = PRICING.build_plan(rate)
            self.assertFalse(plan["writes_production"])
            self.assertEqual(plan["required_plugin_version"], "1.2.0")
            self.assertEqual(plan["default_resolution"], "1k")
            self.assertEqual(plan["required_usage_facts"], ["image_count", "resolution"])
            expression = plan["pricing_templates"][PRICING.MODEL]["billing_setting.billing_expr"]
            self.assertEqual(plan["provider_billing_expr"][f"toapis-image::{PRICING.MODEL}"], expression)
            coefficients = dict(re.findall(r'tier\("(1k|2k|4k)", u\("image_count"\) \* (\d+(?:\.\d+)?)\)', expression))
            self.assertEqual(set(coefficients), {"1k", "2k", "4k"})
            with localcontext() as context:
                context.prec = 70
                for resolution, (cost, sale) in {"1k": ("0.1", "0.2"), "2k": ("0.15", "0.3"), "4k": ("0.2", "0.4")}.items():
                    quote = plan["quotes"][resolution]
                    self.assertEqual(Decimal(quote["cost_estimate_cny_per_image"]), Decimal(cost))
                    self.assertEqual(Decimal(quote["sale_cny_per_image"]), Decimal(sale))
                    self.assertEqual(Decimal(quote["gross_margin_estimate"]), Decimal("0.5"))
                    for actual_count in (1, 2, 4):
                        total_cny = Decimal(coefficients[resolution]) * Decimal(actual_count) * Decimal(rate)
                        self.assertLess(abs(total_cny - Decimal(sale) * actual_count), Decimal("1e-45"))

    def test_snapshot_cas_preserves_legacy_other_models_and_provider_overrides(self):
        original = {"success": True, "data": {"entries": [
            {"model_name": PRICING.MODEL, "version": "opaque-expected-version", "configured": {
                "ModelPrice": 0.031, "ModelRatio": 37.5,
                "billing_setting.billing_mode": "ratio",
                "billing_setting.plugin_billing_expr": {"toapis-image": "old", "other-provider": "unchanged"}}},
            {"model_name": "unrelated", "version": "unchanged", "configured": {"ModelPrice": 123}},
        ]}}
        snapshot = deepcopy(original)
        plan = PRICING.build_plan("7.3", snapshot)
        self.assertEqual(snapshot, original)
        self.assertEqual(len(plan["request"]["changes"]), 1)
        change = plan["request"]["changes"][0]
        self.assertEqual(change["model_name"], PRICING.MODEL)
        self.assertEqual(change["expected_version"], "opaque-expected-version")
        pricing = change["pricing"]
        self.assertEqual((pricing["ModelPrice"], pricing["ModelRatio"]), (0.031, 37.5))
        self.assertEqual(pricing["billing_setting.billing_mode"], "tiered_expr")
        variants = pricing["billing_setting.plugin_billing_expr"]
        self.assertEqual(variants["other-provider"], "unchanged")
        self.assertEqual(variants["toapis-image"], pricing["billing_setting.billing_expr"])
        minimal = {"entries": [{"model_name": PRICING.MODEL, "version": "v1", "configured": {}}]}
        minimal_plan = PRICING.build_plan("7.3", minimal)
        self.assertNotIn("billing_setting.plugin_billing_expr", minimal_plan["request"]["changes"][0]["pricing"])

    def test_invalid_inputs_fail_closed(self):
        for rate in ("0", "-1", "NaN", "sNaN", "Infinity", "abc", "0.009", "1000.001"):
            with self.subTest(rate=rate), self.assertRaisesRegex(ValueError, "USD exchange rate must be finite"):
                PRICING.build_plan(rate)
        for snapshot in ([], {}, {"entries": []}, {"entries": [
            {"model_name": PRICING.MODEL, "configured": {}}]}, {"entries": [
            {"model_name": PRICING.MODEL, "version": "v", "configured": {"billing_setting.plugin_billing_expr": None}}]}):
            with self.subTest(snapshot=snapshot), self.assertRaises(ValueError):
                PRICING.build_plan("7.3", snapshot)

    def test_cli_is_deterministic_and_requires_current_rate(self):
        command = [sys.executable, str(SCRIPT), "--usd-exchange-rate", "7.3"]
        first = subprocess.run(command, check=True, capture_output=True, text=True)
        second = subprocess.run(command, check=True, capture_output=True, text=True)
        self.assertEqual(first.stdout, second.stdout)
        self.assertEqual(json.loads(first.stdout), PRICING.build_plan("7.3"))
        self.assertEqual(first.stderr, "")
        missing = subprocess.run([sys.executable, str(SCRIPT)], capture_output=True, text=True)
        self.assertEqual(missing.returncode, 2)
        self.assertEqual(missing.stdout, "")
        self.assertIn("--usd-exchange-rate", missing.stderr)


if __name__ == "__main__":
    unittest.main()
