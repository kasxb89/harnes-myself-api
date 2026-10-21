"""Regression cases for the reviewed Blackrx cost-plus-second price contract."""

from decimal import Decimal, localcontext
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys
import unittest


SCRIPT = Path(__file__).with_name("blackrxai-video-pricing.py")
SPEC = importlib.util.spec_from_file_location("blackrxai_video_pricing", SCRIPT)
PRICING = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PRICING)


class BlackrxVideoPricingTest(unittest.TestCase):
    def test_all_reviewed_costs_and_sixty_one_sale_quotes(self):
        plan = PRICING.build_plan("7.3")
        fixed = {
            "lec-mj-seedance-2-5-cd-720p": ((30,), "5.5"),
            "sd-25-900-2": ((30,), "6.5"),
            "k2-sd2.5-720p": ((30,), "8.6"),
            "lec-sz-sd2-full-480p": ((30,), "8.5"),
            "sd2-th-2.0-720p": ((15,), "2.4"),
            "bt-seedance-2.0": ((10, 15), "1.5"),
            "lec-rs-seedance-2-0-fast": ((15,), "2.5"),
            "sd20-933-720p": ((15,), "3.2"),
            "sd2-hn2-480-pro": ((15,), "4.3"),
            "v3-sd2.0-933-720p": ((15,), "4.5"),
            "sd-20-9/3/3-1": ((15,), "5"),
            "sd-20-903": ((15,), "1.8"),
        }
        per_second = {"video-sd25-480p": "0.6", "model_5": "0.43", "sd2.5-720p-x2": "0.52"}
        expected_models = set(fixed) | set(per_second)
        self.assertEqual(set(plan["pricing"]), expected_models)
        self.assertEqual((plan["model_count"], plan["quote_count"]), (15, 61))
        self.assertNotIn("video-sd25-720p", plan["pricing"])
        self.assertFalse(plan["writes_production"])
        for model, entry in plan["pricing"].items():
            durations = fixed[model][0] if model in fixed else tuple(range(15, 31))
            self.assertEqual(entry["allowed_seconds"], list(durations))
            self.assertEqual([quote["seconds"] for quote in entry["quotes"]], list(durations))
            for quote in entry["quotes"]:
                with self.subTest(model=model, seconds=quote["seconds"]):
                    seconds = Decimal(quote["seconds"])
                    expected_cost = Decimal(fixed[model][1]) if model in fixed else Decimal(per_second[model]) * seconds
                    margin = seconds * Decimal("0.1")
                    self.assertEqual(Decimal(quote["cost_cny"]), expected_cost)
                    self.assertEqual(Decimal(quote["upstream_credits"]), expected_cost * 10)
                    self.assertEqual(Decimal(quote["sale_cny"]), expected_cost + margin)
                    self.assertEqual(Decimal(quote["margin_cny"]), margin)
        bt = plan["pricing"]["bt-seedance-2.0"]
        self.assertEqual(bt["default_seconds"], 15)
        self.assertEqual([quote["sale_cny"] for quote in bt["quotes"]], ["2.5", "3"])
        self.assertTrue(all(entry["default_seconds"] == entry["allowed_seconds"][-1] for entry in plan["pricing"].values()))

    def test_expression_dollars_match_quotes_and_provider_scope(self):
        with localcontext() as context:
            context.prec = 70
            for rate in ("0.01", "7.3", "7.123456789", "1000"):
                plan = PRICING.build_plan(rate)
                self.assertEqual(len(plan["provider_billing_expr"]), 15)
                for model, entry in plan["pricing"].items():
                    expression = plan["provider_billing_expr"][f"blackrxai-video::{model}"]
                    self.assertEqual(expression, plan["pricing_templates"][model]["billing_setting.billing_expr"])
                    match = re.fullmatch(r'tier\("base", (?:(\d+(?:\.\d+)?) \+ )?u\("seconds"\) \* (\d+(?:\.\d+)?)\)', expression)
                    self.assertIsNotNone(match, expression)
                    constant, coefficient = (Decimal(value or "0") for value in match.groups())
                    for quote in entry["quotes"]:
                        dollars = constant + Decimal(quote["seconds"]) * coefficient
                        self.assertLess(abs(dollars * Decimal(rate) - Decimal(quote["sale_cny"])), Decimal("1e-45"))
                        self.assertLess(abs(Decimal(quote["sale_usd"]) * Decimal(rate) - Decimal(quote["sale_cny"])), Decimal("1e-45"))

    def test_invalid_exchange_rates_rejected(self):
        for rate in ("0", "-1", "NaN", "sNaN", "Infinity", "-Infinity", "abc", "0.009", "1000.001"):
            with self.subTest(rate=rate), self.assertRaisesRegex(ValueError, "USD exchange rate must be finite"):
                PRICING.build_plan(rate)

    def test_cli_is_deterministic_json_and_requires_current_rate(self):
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
