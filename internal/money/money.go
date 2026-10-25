package money

import (
	"fmt"
	"math/big"
	"regexp"
)

var decimalPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// Parse reads a plain decimal amount such as "12.50". Fractions, exponents and
// signs are rejected.
func Parse(s string) (*big.Rat, error) {
	if !decimalPattern.MatchString(s) {
		return nil, fmt.Errorf("invalid amount %q", s)
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid amount %q", s)
	}
	return r, nil
}

// Format renders r with exactly the given number of decimals, rounding down.
func Format(r *big.Rat, decimals int) string {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	scaled := new(big.Int).Mul(r.Num(), scale)
	scaled.Quo(scaled, r.Denom())
	return new(big.Rat).SetFrac(scaled, scale).FloatString(decimals)
}

// Decimals reports how many digits follow the decimal point in s.
func Decimals(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return len(s) - i - 1
		}
	}
	return 0
}

// Equal compares two decimal strings by value, so "10" equals "10.0000000".
func Equal(a, b string) bool {
	ra, errA := Parse(a)
	rb, errB := Parse(b)
	return errA == nil && errB == nil && ra.Cmp(rb) == 0
}
