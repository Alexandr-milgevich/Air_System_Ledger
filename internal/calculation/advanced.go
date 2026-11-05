package calculation

import (
	"errors"
	"fmt"
	"math/big"
)

// Pi is a frozen decimal value, so repeated calculations remain deterministic.
const Pi = "3.1415926535897932384626433832795028841971"

// Geometry computes chargeable surface area in m² from dimensions in mm and length in m.
// The coefficient is stored at 10 decimal places to fit project_work_items.coefficient.
// Geometry uses full precision for the area and rounds only at the NUMERIC(20,8) storage boundary.
func Geometry(mode, length, a, b, price string) (Result, error) {
	l, err := Parse(length, 8)
	if err != nil {
		return Result{}, fmt.Errorf("length: %w", err)
	}
	p, err := Parse(price, 4)
	if err != nil {
		return Result{}, fmt.Errorf("price: %w", err)
	}
	width, err := Parse(a, 4)
	if err != nil {
		return Result{}, fmt.Errorf("first dimension: %w", err)
	}
	if width.Sign() <= 0 {
		return Result{}, errors.New("dimension must be positive")
	}
	thousand := big.NewRat(1000, 1)
	var factor *big.Rat
	switch mode {
	case "geometry_rectangular":
		height, e := Parse(b, 4)
		if e != nil {
			return Result{}, fmt.Errorf("second dimension: %w", e)
		}
		if height.Sign() <= 0 {
			return Result{}, errors.New("dimension must be positive")
		}
		factor = new(big.Rat).Mul(big.NewRat(2, 1), new(big.Rat).Add(width, height))
	case "geometry_round":
		pi, _ := new(big.Rat).SetString(Pi)
		factor = new(big.Rat).Mul(width, pi)
	default:
		return Result{}, errors.New("unsupported geometry")
	}
	factor.Quo(factor, thousand)
	area := new(big.Rat).Mul(l, factor)
	areaText := Fixed(area, 8)
	stored, _ := new(big.Rat).SetString(areaText)
	return Result{Calculated: areaText, Billable: areaText, Amount: Fixed(new(big.Rat).Mul(stored, p), 2), Coefficient: Fixed(factor, 10)}, nil
}

// Linked computes a dependent area from an already stored calculated area.
// Its factor is dimensionless; amount is rounded from the stored 8-decimal quantity.
func Linked(sourceArea, factor, price string) (Result, error) {
	return Compute("coefficient", sourceArea, factor, price)
}
