package controller

import (
	"maps"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/shopspring/decimal"
)

// The administration form selects complete existing specifications, not JSON.
// This is metadata only and does not decode, send or fund a generation request.
func populateOfficialReferenceSpecifications(snapshot *model.ModelConfigurationSnapshot) {
	for i := range snapshot.Entries {
		entry := &snapshot.Entries[i]
		def := entry.Definition
		if entry.Draft != nil {
			def = entry.Draft.Definition
		}
		seen := map[string]bool{}
		rows := []model.ModelOfficialReferenceRow{}
		add := func(row model.ModelOfficialReferenceRow) {
			key := model.OfficialReferenceRowKey(def.Kind, row.Params, row.Direction, row.Unit)
			if key != "" && !seen[key] {
				seen[key] = true
				rows = append(rows, row)
			}
		}
		for _, row := range entry.ReferenceSpecifications {
			add(row)
		}
		if def.Kind == "text" {
			for _, direction := range []string{"input", "output"} {
				add(model.ModelOfficialReferenceRow{Params: map[string]any{}, Direction: direction, Unit: "CNY / million tokens"})
			}
		} else {
			parameters := model.ProjectModelConfigurationParameters(entry.LegacyParameters, def.Parameters)
			for _, spec := range unifiedRetailSpecifications(parameters) {
				if _, exists := spec["mode"]; !exists {
					spec["mode"] = "text_to_" + def.Kind
				}
				add(model.ModelOfficialReferenceRow{Params: spec, Unit: "CNY / " + map[string]string{"image": "image", "video": "second"}[def.Kind]})
			}
		}
		if def.OfficialReferences != nil {
			for _, row := range def.OfficialReferences.Rows {
				add(row)
			}
		}
		entry.ReferenceSpecifications = rows
	}
}

func applyOfficialPriceReferences(name, kind string, config *model.ModelOfficialReferenceConfiguration, rows []map[string]any) []map[string]any {
	references := model.BuiltinModelOfficialReferenceRows(name)
	if config != nil {
		switch config.Mode {
		case "disabled":
			references = nil
		case "override":
			references = config.Rows
		}
	}
	bySpecification := map[string]*model.OfficialPriceReference{}
	for _, row := range references {
		if row.Reference != nil && model.ValidateOfficialReferenceRow(kind, row) == nil {
			bySpecification[model.OfficialReferenceRowKey(kind, row.Params, row.Direction, row.Unit)] = row.Reference
		}
	}
	result := make([]map[string]any, 0, len(rows))
	for _, original := range rows {
		row := maps.Clone(original)
		delete(row, "official_reference")
		params, _ := row["params"].(map[string]any)
		direction, _ := row["direction"].(string)
		unit, _ := row["unit"].(string)
		key := model.OfficialReferenceRowKey(kind, params, direction, unit)
		if reference := bySpecification[key]; key != "" && reference != nil {
			// Marshal only the reviewed public fields, never the administration
			// definition, procurement evidence, routes or credentials.
			row["official_reference"] = *reference
		}
		result = append(result, row)
	}
	return result
}

// A legacy token tariff has an exact base unit price. Complex expressions keep
// their existing engine metadata; this display path never infers a generic
// per-token price from a few sampled evaluations or a different context tier.
func unifiedTextReferenceRetailRows(record model.PublishedModelConfiguration, price model.Pricing) []map[string]any {
	if record.Definition.Kind != "text" {
		return nil
	}
	if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) || operation_setting.USDExchangeRate <= 0 || math.IsNaN(operation_setting.USDExchangeRate) || math.IsInf(operation_setting.USDExchangeRate, 0) || price.ModelRatio < 0 || math.IsNaN(price.ModelRatio) || math.IsInf(price.ModelRatio, 0) || price.CompletionRatio < 0 || math.IsNaN(price.CompletionRatio) || math.IsInf(price.CompletionRatio, 0) {
		return nil
	}
	var input, output decimal.Decimal
	if price.BillingMode == billing_setting.BillingModeTieredExpr {
		inputUSD, outputUSD, ok := linearTextTariff(price.BillingExpr)
		if !ok {
			return nil
		}
		input = inputUSD.Mul(decimal.NewFromFloat(operation_setting.USDExchangeRate))
		output = outputUSD.Mul(decimal.NewFromFloat(operation_setting.USDExchangeRate))
	} else {
		if _, explicit := record.Definition.Pricing["ModelRatio"]; !explicit || price.QuotaType != 0 {
			return nil
		}
		input = decimal.NewFromFloat(price.ModelRatio).Mul(decimal.NewFromInt(1000000)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(decimal.NewFromFloat(operation_setting.USDExchangeRate))
		output = input.Mul(decimal.NewFromFloat(price.CompletionRatio))
	}
	return []map[string]any{
		{"params": map[string]any{}, "direction": "input", "unit": "CNY / million tokens", "retail_cny": input.String()},
		{"params": map[string]any{}, "direction": "output", "unit": "CNY / million tokens", "retail_cny": output.String()},
	}
}

// Extract only an algebraically linear standard text tariff from the existing
// expression AST. Request/context tiers, fixed fees and nonlinear formulas are
// deliberately excluded; a few sampled evaluations cannot prove a unit price.
func linearTextTariff(expression string) (decimal.Decimal, decimal.Decimal, bool) {
	_, body := billingexpr.ParseExprVersion(expression)
	tree, err := parser.Parse(body)
	if err != nil {
		return decimal.Zero, decimal.Zero, false
	}
	type term struct {
		constant     decimal.Decimal
		coefficients map[string]decimal.Decimal
	}
	scale := func(value term, multiplier decimal.Decimal) term {
		result := term{constant: value.constant.Mul(multiplier), coefficients: map[string]decimal.Decimal{}}
		for key, coefficient := range value.coefficients {
			result.coefficients[key] = coefficient.Mul(multiplier)
		}
		return result
	}
	var extract func(ast.Node) (term, bool)
	extract = func(node ast.Node) (term, bool) {
		value := term{coefficients: map[string]decimal.Decimal{}}
		switch part := node.(type) {
		case *ast.IntegerNode:
			value.constant = decimal.NewFromInt(int64(part.Value))
			return value, true
		case *ast.FloatNode:
			if math.IsNaN(part.Value) || math.IsInf(part.Value, 0) {
				return value, false
			}
			value.constant = decimal.NewFromFloat(part.Value)
			return value, true
		case *ast.IdentifierNode:
			switch part.Value {
			case "p", "c", "cr", "cc", "cc1h", "img", "ai", "ao":
				value.coefficients[part.Value] = decimal.NewFromInt(1)
				return value, true
			}
		case *ast.UnaryNode:
			operand, ok := extract(part.Node)
			if ok && part.Operator == "-" {
				return scale(operand, decimal.NewFromInt(-1)), true
			}
			if ok && part.Operator == "+" {
				return operand, true
			}
		case *ast.CallNode:
			if callee, ok := part.Callee.(*ast.IdentifierNode); ok && callee.Value == "tier" && len(part.Arguments) == 2 {
				if _, label := part.Arguments[0].(*ast.StringNode); label {
					return extract(part.Arguments[1])
				}
			}
		case *ast.BinaryNode:
			left, ok := extract(part.Left)
			if !ok {
				return value, false
			}
			right, ok := extract(part.Right)
			if !ok {
				return value, false
			}
			switch part.Operator {
			case "+", "-":
				if part.Operator == "-" {
					right = scale(right, decimal.NewFromInt(-1))
				}
				left.constant = left.constant.Add(right.constant)
				for key, coefficient := range right.coefficients {
					left.coefficients[key] = left.coefficients[key].Add(coefficient)
				}
				return left, true
			case "*":
				if len(left.coefficients) == 0 {
					return scale(right, left.constant), true
				}
				if len(right.coefficients) == 0 {
					return scale(left, right.constant), true
				}
			case "/":
				if len(right.coefficients) == 0 && !right.constant.IsZero() {
					return scale(left, decimal.NewFromInt(1).Div(right.constant)), true
				}
			}
		}
		return value, false
	}
	value, ok := extract(tree.Node)
	input, output := value.coefficients["p"], value.coefficients["c"]
	return input, output, ok && value.constant.IsZero() && !input.IsNegative() && !output.IsNegative()
}
