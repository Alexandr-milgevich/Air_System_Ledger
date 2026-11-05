// Package calculation calculates chargeable quantities and amounts without floating point.
package calculation

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var decimalPattern = regexp.MustCompile(`^\+?\d+(?:[\.,]\d+)?$`)

// Parse accepts nonnegative decimal quantities and rejects scientific notation.
func Parse(raw string, maxScale int) (*big.Rat, error) {
	raw = strings.TrimSpace(raw)
	if !decimalPattern.MatchString(raw) {
		return nil, errors.New("expected a nonnegative decimal number")
	}
	raw = strings.ReplaceAll(raw, ",", ".")
	pieces := strings.SplitN(raw, ".", 2)
	if len(pieces) == 2 && len(pieces[1]) > maxScale {
		return nil, fmt.Errorf("maximum %d decimal places", maxScale)
	}
	n, ok := new(big.Rat).SetString(raw)
	if !ok || n.Sign() < 0 {
		return nil, errors.New("invalid decimal")
	}
	return n, nil
}

// Fixed returns a decimal string rounded half-up to the specified scale.
func Fixed(r *big.Rat, scale int) string {
	mult := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(mult))
	numerator, denominator := scaled.Num(), scaled.Denom()
	q, remainder := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(denominator) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if scale == 0 {
		return q.String()
	}
	digits := q.String()
	for len(digits) <= scale {
		digits = "0" + digits
	}
	return digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
}

type Result struct{ Calculated, Billable, Amount, Coefficient string }

// Compute supports a direct billed quantity or length/count multiplied by an area coefficient.
// All row amounts are rounded to kopecks using half-up rounding.
func Compute(mode, qty, factor, price string) (Result, error) {
	q, err := Parse(qty, 8)
	if err != nil {
		return Result{}, fmt.Errorf("quantity: %w", err)
	}
	p, err := Parse(price, 4)
	if err != nil {
		return Result{}, fmt.Errorf("price: %w", err)
	}
	var derived *big.Rat
	var coef string
	switch mode {
	case "direct":
		derived = new(big.Rat).Set(q)
	case "coefficient":
		c, e := Parse(factor, 10)
		if e != nil {
			return Result{}, fmt.Errorf("coefficient: %w", e)
		}
		derived = new(big.Rat).Mul(q, c)
		coef = Fixed(c, 10)
	default:
		return Result{}, errors.New("unknown calculation mode")
	}
	// NUMERIC(20,8) storage follows the same 8-place intermediate rounding.
	derivedText := Fixed(derived, 8)
	stored, _ := new(big.Rat).SetString(derivedText)
	amount := new(big.Rat).Mul(stored, p)
	return Result{Calculated: derivedText, Billable: derivedText, Amount: Fixed(amount, 2), Coefficient: coef}, nil
}
