package calculation

import (
	"errors"
	"fmt"
	"math/big"
)

// AggregateArea totals stored m² quantities, subtracts exclusions, then applies a
// nonnegative multiplier and price. Amount is computed from stored 8-place area.
// A negative net area is rejected instead of silently saving negative quantities.
func AggregateArea(included, excluded []string, multiplier, price string) (Result, error) {
	if len(included) == 0 {
		return Result{}, errors.New("at least one included source required")
	}
	factor, err := Parse(multiplier, 10)
	if err != nil {
		return Result{}, fmt.Errorf("multiplier: %w", err)
	}
	p, err := Parse(price, 4)
	if err != nil {
		return Result{}, fmt.Errorf("price: %w", err)
	}
	net := new(big.Rat)
	for _, v := range included {
		a, e := Parse(v, 8)
		if e != nil {
			return Result{}, fmt.Errorf("included area: %w", e)
		}
		net.Add(net, a)
	}
	for _, v := range excluded {
		a, e := Parse(v, 8)
		if e != nil {
			return Result{}, fmt.Errorf("excluded area: %w", e)
		}
		net.Sub(net, a)
	}
	if net.Sign() < 0 {
		return Result{}, errors.New("excluded area exceeds included area")
	}
	net.Mul(net, factor)
	area := Fixed(net, 8)
	stored, _ := new(big.Rat).SetString(area)
	return Result{Calculated: area, Billable: area, Amount: Fixed(new(big.Rat).Mul(stored, p), 2), Coefficient: Fixed(factor, 10)}, nil
}
