"""Offline regressions for the disabled CodeBloom review plan."""

import ast
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest import mock


SCRIPT = Path(__file__).with_name("codebloom-plan.py")
SPEC = importlib.util.spec_from_file_location("codebloom_plan", SCRIPT)
PLAN = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PLAN)


class CodeBloomPlanTests(unittest.TestCase):
    def setUp(self):
        self.catalog = json.loads(PLAN.CATALOG_PATH.read_text(encoding="utf-8"))

    def test_three_independent_credential_purposes_are_disabled(self):
        plan = PLAN.build_plan(self.catalog)
        self.assertEqual(plan["status"], "REVIEW_ONLY_DISABLED_NOT_APPLIED")
        self.assertEqual(len(plan["channels"]), 3)
        self.assertEqual([channel["type"] for channel in plan["channels"]], [1, 1, 61])
        self.assertEqual([channel["credential_env"] for channel in plan["channels"]], ["CODEBLOOM_GPT_KEY", "CODEBLOOM_CN_KEY", "CODEBLOOM_IMAGE_KEY"])
        self.assertEqual([len(channel["models"].split(",")) for channel in plan["channels"]], [6, 21, 10])
        for channel in plan["channels"]:
            self.assertEqual(channel["status"], 2)
            self.assertFalse(channel["activation_ready"])
            self.assertFalse(channel["credentials_configured"])
            self.assertNotIn("key", channel)
            self.assertEqual(channel["base_url"], "https://codebloom.club")
            mapping = json.loads(channel["model_mapping"])
            self.assertEqual(set(mapping), set(channel["models"].split(",")))
            for public, raw in mapping.items():
                self.assertEqual(public, "ys-" + raw)
        self.assertTrue(all(model["id"] == model["name"] == "ys-" + model["upstream_id"] for model in self.catalog["models"]))
        self.assertEqual(json.loads(plan["channels"][2]["setting"]), {"task_plugin_key": "codebloom-image"})
        self.assertEqual(len(plan["abilities"]), 37)
        self.assertTrue(all(ability["enabled"] is False for ability in plan["abilities"]))
        self.assertEqual(len({ability["model"] for ability in plan["abilities"]}), 37)
        self.assertEqual({ability["group"] for ability in plan["abilities"]}, {"default"})

    def test_approved_retail_never_writes_cash_cost_group_ratio_or_old_prices(self):
        plan = PLAN.build_plan(self.catalog)
        self.assertEqual(len(plan["retail_prices"]), 37)
        self.assertEqual(plan["retail_pricing_status"], "approved")
        self.assertEqual(plan["cash_procurement_status"], "credit_purchase_exchange_rate_unknown")
        self.assertEqual(plan["option_updates"], [])
        self.assertEqual(plan["cost_rule_updates"], [])
        self.assertEqual(plan["existing_model_updates"], [])
        for model in plan["cost_review"]:
            self.assertEqual(model["cost_basis"], "account_credit_snapshot")
            self.assertEqual(model["currency"], "CNY_ACCOUNT_CREDIT")
            if model["retail_account_multiplier"] is not None:
                self.assertIsNone(model["retail_cny"])
                self.assertIn(model["retail_account_multiplier"], (0.45, 0.70))
            else:
                self.assertIsNotNone(model["retail_cny"])
                self.assertEqual(model["retail_markup_cny"], .10)
            self.assertIsNone(model["cash_cost_cny"])
            self.assertFalse(model["activation_ready"])
            self.assertTrue(model["cost_components_include_account_multiplier_once"])
            self.assertFalse(model["base_expression_includes_account_multiplier"])
            self.assertTrue(all(component["retail_cny"] is not None for component in model["cost_components"]))
        self.assertNotIn("GroupRatio", json.dumps(plan))
        for item in plan["retail_prices"]:
            self.assertFalse(item["gateway_usd_expression_generated"])
            self.assertFalse(item["activation_ready"])

    def test_account_environment_values_are_not_read_or_printed(self):
        sentinel = "sentinel-sensitive-value-must-not-be-in-output"
        with mock.patch.dict(os.environ, {name: sentinel for name, *_ in PLAN.GROUPS}):
            plan = PLAN.build_plan()
        self.assertFalse(plan["credentials_read"])
        self.assertNotIn(sentinel, json.dumps(plan))
        self.assertEqual(plan["network_requests"], 0)
        self.assertEqual(plan["database_writes"], 0)
        self.assertEqual(plan["paid_requests"], 0)
        tree = ast.parse(SCRIPT.read_text(encoding="utf-8"))
        imports = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                imports.update(name.name.split(".")[0] for name in node.names)
            if isinstance(node, ast.ImportFrom):
                imports.add(node.module.split(".")[0])
        self.assertFalse(imports & {"os", "socket", "urllib", "requests", "http", "sqlite3", "subprocess"})

    def test_short_text_rates_include_the_multiplier_exactly_once(self):
        # Independent reviewed-account table: uncached input, output, cache read.
        expected = {
            "gpt-5.6-terra": (.5, 3, .05), "gpt-5.6-sol": (1.25, 7.5, .125),
            "gpt-6.1-terra": (.25, 1.25, .0125), "gpt-6-sol": (.5, 2.5, .05),
            "gpt-6-astra": (2.5, 12.5, .25), "gpt-6.1-sol": (.5, 2.5, .025),
            "deepseek-v4.1-flash": (.5, 2, .01), "deepseek-v4.1-flash-1017": (.5, 2, .01),
            "minimax-m3.1-flash": (.075, .3, .0075), "step-5-preview": (3.5, 10, .35),
            "mimo-v2.6-pro": (1.5, 3, .0125), "glm-5.3-flash": (.4, 1.4, .115),
            "deepseek-v4-pro": (2.25, 6.75, .075), "glm-5.3-flashx": (1, 3.5, .285),
            "kimi-k2.7-code": (3.25, 13.5, .325), "hy3": (.5, 2, .15),
            "qwen3.8-max": (6, 18, 7.5), "deepseek-v4-flash-0731": (.75, 2.25, .025),
            "deepseek-v4-pro-0813": (2.25, 6.75, .075), "kimi-k3.1-pro": (6.5, 34, .65),
            "kimi-k2.6": (3.25, 13.5, .325), "deepseek-v4-flash": (.75, 2.25, .025),
            "kimi-k3": (10, 50, 1), "qwen3.7-max": (6, 18, .75),
            "kimi-k2.7": (3.25, 13.5, .325), "glm-5.1": (4, 14, .4),
            "qwen3.8-flash": (.4, 1.35, .1),
        }
        models = {model["upstream_id"]: model for model in self.catalog["models"] if model["kind"] == "text"}
        self.assertEqual(set(models), set(expected))
        for name, rates in expected.items():
            for direction, want in zip(("input", "output", "cache_read"), rates):
                candidates = [row for row in models[name]["cost_components"] if row["direction"] == direction and row["params"].get("pricing_tier") not in ("long", "peak")]
                self.assertEqual(len(candidates), 1, (name, direction))
                self.assertAlmostEqual(candidates[0]["account_credit_cny"], want, places=12)
                model = models[name]
                approved_multiplier = .45 if model["credential_env"] == "CODEBLOOM_GPT_KEY" else .70
                self.assertEqual(model["retail_account_multiplier"], approved_multiplier)
                self.assertAlmostEqual(candidates[0]["retail_cny"], want / model["account_multiplier"] * approved_multiplier, places=12)
        gpt = models["gpt-6-sol"]
        example = next(row for row in gpt["cost_components"] if row["direction"] == "input" and row["params"]["pricing_tier"] == "short")
        self.assertEqual(example["account_credit_cny"], .5)
        self.assertEqual(example["retail_cny"], .9)
        self.assertNotEqual(example["retail_cny"], .5 * 1.2)

    def test_raw_long_context_peak_and_cache_conditions_are_preserved(self):
        models = {model["model"]: model for model in PLAN.build_plan()["cost_review"]}
        terra = models["ys-gpt-6.1-terra"]
        self.assertIn("len < 272000", terra["base_billing_expression"])
        self.assertIn("cc12h", terra["base_billing_expression"])
        context = terra["cost_conditions"]["context_pricing"]
        self.assertEqual(context["threshold"], 272000)
        self.assertFalse(context["is_capacity_limit"])
        for name in ("deepseek-v4-flash", "deepseek-v4-flash-0731"):
            model = models["ys-" + name]
            peak = model["cost_conditions"]["shanghai_weekday_peak"]
            self.assertEqual(peak["timezone"], "Asia/Shanghai")
            self.assertEqual(peak["hour_windows"], [{"start_inclusive": 9, "end_exclusive": 12}, {"start_inclusive": 14, "end_exclusive": 18}])
            self.assertEqual((peak["weekday_min_inclusive"], peak["weekday_max_exclusive"]), (1, 6))
            self.assertFalse(peak["holiday_exclusion"])
            for direction in ("input", "output", "cache_read"):
                rows = {row["params"]["pricing_tier"]: row["account_credit_cny"] for row in model["cost_components"] if row["direction"] == direction}
                self.assertEqual(rows["peak"], rows["off_peak"] * 2)
                retail = {row["params"]["pricing_tier"]: row["retail_cny"] for row in model["cost_components"] if row["direction"] == direction}
                self.assertAlmostEqual(retail["peak"], retail["off_peak"] * 2, places=12)
        self.assertEqual(models["ys-deepseek-v4.1-flash"]["cost_conditions"], {})
        for model in models.values():
            if model["retail_account_multiplier"] is None:
                continue
            for cost in model["cost_components"]:
                self.assertAlmostEqual(cost["retail_cny"], cost["account_credit_cny"] / model["account_multiplier"] * model["retail_account_multiplier"], places=12)
        long_cache = next(row for row in terra["cost_components"] if row["direction"] == "cache_write_12h" and row["params"]["pricing_tier"] == "long")
        self.assertEqual(long_cache["retail_cny"], 2.25)

    def test_image_units_are_not_conflated_and_unverified_controls_stay_absent(self):
        models = {model["upstream_id"]: model for model in self.catalog["models"]}
        expected = {
            "grok-imagine-image-2.0": (.15, "request"), "grok-imagine-image": (.1125, "request"),
            "gemini-3.1-flash-image-preview": (.15, "request"), "nano-banana-2": (.1125, "request"),
            "gpt-image-2.5-sunburst": (.075, "image"), "gemini-nano-banana-2.1": (.225, "request"),
            "gpt-image-2.5-flare": (.075, "image"), "gpt-image-2k": (.1125, "image"),
            "nano-banana-pro": (.225, "request"), "gemini-3-pro-image-preview": (.15, "request"),
        }
        for name, (want, direction) in expected.items():
            model = models[name]
            self.assertEqual(len(model["cost_components"]), 1)
            cost = model["cost_components"][0]
            self.assertEqual(cost["account_credit_cny"], want)
            self.assertAlmostEqual(cost["retail_cny"], want + .10, places=12)
            self.assertEqual(model["retail_cny"], cost["retail_cny"])
            self.assertEqual(cost["direction"], direction)
            if direction == "request":
                self.assertEqual(model["verified_request_options"], {"stream": False})
        self.assertEqual(models["gpt-image-2k"]["observed_output"], {"width": 1024, "height": 1024, "format": "png"})
        self.assertEqual(models["gpt-image-2k"]["verified_request_options"], {"size": "1024x1024", "n": 1})

    def test_plan_does_not_mutate_the_reviewed_snapshot(self):
        before = copy.deepcopy(self.catalog)
        plan = PLAN.build_plan(self.catalog)
        plan["cost_review"][0]["cost_components"][0]["params"]["injected"] = True
        plan["cost_review"][0]["pending_reasons"][0] = "changed"
        self.assertEqual(self.catalog, before)

    def test_exclusions_match_all_nine_previous_failures(self):
        self.assertEqual({item["id"] for item in self.catalog["excluded"]}, {"MiniMax-M3", "MiniMax-M2.7", "MiniMax-M2.7-highspeed", "mimo-v2.5", "hy4", "qwen3.8-max-0902", "glm-5.2", "glm-5.3", "hy4-preview"})

    def test_unqualified_mutations_fail_closed(self):
        cases = {
            "retail": lambda model: model.update(retail_cny=1),
            "active": lambda model: model.update(activation_ready=True),
            "cash": lambda model: model.update(cash_cost_cny=.1),
            "capacity": lambda model: model.update(context_window=272000),
            "model_id": lambda model: model.update(id=model["upstream_id"]),
            "display_name": lambda model: model.update(name=model["upstream_id"]),
            "multiplier": lambda model: model.update(account_multiplier=.3),
            "retail_multiplier": lambda model: model.update(retail_account_multiplier=.6),
            "missing_retail": lambda model: model["cost_components"][0].update(retail_cny=None),
            "credentials": lambda model: model.update(credential_env="UNKNOWN_ENV"),
            "vision": lambda model: model["capability_status"].update(vision_input="verified"),
            "nonfinite_cost": lambda model: model["cost_components"][0].update(account_credit_cny=float("nan")),
        }
        for name, mutate in cases.items():
            with self.subTest(name=name):
                catalog = copy.deepcopy(self.catalog)
                mutate(catalog["models"][0])
                with self.assertRaises(ValueError):
                    PLAN.build_plan(catalog)

    def test_cli_only_prints_review_and_rejects_apply(self):
        original = hashlib.sha256(PLAN.CATALOG_PATH.read_bytes()).hexdigest()
        result = subprocess.run([sys.executable, str(SCRIPT)], check=True, capture_output=True, text=True, timeout=10)
        self.assertEqual(json.loads(result.stdout)["status"], "REVIEW_ONLY_DISABLED_NOT_APPLIED")
        self.assertEqual(result.stderr, "")
        refused = subprocess.run([sys.executable, str(SCRIPT), "--apply"], capture_output=True, text=True, timeout=10)
        self.assertEqual(refused.returncode, 2)
        self.assertEqual(refused.stdout, "")
        self.assertEqual(original, hashlib.sha256(PLAN.CATALOG_PATH.read_bytes()).hexdigest())


if __name__ == "__main__":
    unittest.main()
