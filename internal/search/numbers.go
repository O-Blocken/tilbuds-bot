package search

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Op is a typed comparison operator for structured filters.
type Op int

// Supported comparison operators.
const (
	OpLT Op = iota
	OpLE
	OpEQ
	OpGE
	OpGT
)

// String returns the canonical operator spelling.
func (o Op) String() string {
	switch o {
	case OpLT:
		return "<"
	case OpLE:
		return "<="
	case OpEQ:
		return "="
	case OpGE:
		return ">="
	case OpGT:
		return ">"
	}
	return "?"
}

// Holds reports whether a op b.
func (o Op) Holds(a, b int64) bool {
	switch o {
	case OpLT:
		return a < b
	case OpLE:
		return a <= b
	case OpEQ:
		return a == b
	case OpGE:
		return a >= b
	case OpGT:
		return a > b
	}
	return false
}

// splitOp parses a leading comparison operator from s. A missing operator
// means equality. The second return is the remaining value text.
func splitOp(s string) (Op, string) {
	switch {
	case strings.HasPrefix(s, "<="):
		return OpLE, strings.TrimSpace(s[2:])
	case strings.HasPrefix(s, ">="):
		return OpGE, strings.TrimSpace(s[2:])
	case strings.HasPrefix(s, "<"):
		return OpLT, strings.TrimSpace(s[1:])
	case strings.HasPrefix(s, ">"):
		return OpGT, strings.TrimSpace(s[1:])
	case strings.HasPrefix(s, "="):
		return OpEQ, strings.TrimSpace(s[1:])
	}
	return OpEQ, s
}

// Money is an amount in Danish øre (integer minor units) so price
// comparisons never depend on floating-point rounding.
type Money int64

// ParseMoney parses "49", "49,95" or "49.95" into øre. Both decimal
// commas and decimal points are accepted. At most two decimals are
// allowed; anything else is an error rather than a silent re-reading.
func ParseMoney(s string) (Money, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	if !hasFrac {
		whole, frac, hasFrac = strings.Cut(s, ",")
	}
	whole = strings.TrimSpace(whole)
	frac = strings.TrimSpace(frac)
	if whole == "" || !allDigits(whole) {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	wholePart, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	// Amounts are stored in øre: reject values whose conversion to minor units
	// would overflow int64 instead of wrapping to a negative price.
	if wholePart > (math.MaxInt64-99)/100 {
		return 0, fmt.Errorf("invalid amount %q: too large", s)
	}
	var fracPart int64
	if hasFrac {
		if frac == "" || len(frac) > 2 || !allDigits(frac) {
			return 0, fmt.Errorf("invalid amount %q: use at most two decimals", s)
		}
		for len(frac) < 2 {
			frac += "0"
		}
		fracPart, err = strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid amount %q", s)
		}
	}
	return Money(wholePart*100 + fracPart), nil
}

// Kr renders the amount as Danish kroner with a decimal comma.
func (m Money) Kr() string {
	sign := ""
	ore := int64(m)
	if ore < 0 {
		sign = "-"
		ore = -ore
	}
	return fmt.Sprintf("%s%d,%02d kr", sign, ore/100, ore%100)
}

// Percentage is a rate in permille (25% = 250) for deterministic
// discount comparisons.
type Percentage int64

// ParsePercentage parses "25", "25%" or "25,5%" as a percentage. The
// percent sign is optional; at most one decimal is accepted.
func ParsePercentage(s string) (Percentage, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "%")
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty percentage")
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	if !hasFrac {
		whole, frac, hasFrac = strings.Cut(s, ",")
	}
	whole = strings.TrimSpace(whole)
	frac = strings.TrimSpace(frac)
	if whole == "" || !allDigits(whole) {
		return 0, fmt.Errorf("invalid percentage %q", s)
	}
	wholePart, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid percentage %q", s)
	}
	var fracPart int64
	if hasFrac {
		if frac == "" || len(frac) > 1 || !allDigits(frac) {
			return 0, fmt.Errorf("invalid percentage %q: use at most one decimal", s)
		}
		fracPart, err = strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid percentage %q", s)
		}
	}
	if wholePart > 100 {
		return 0, fmt.Errorf("percentage %q out of range 0-100", s)
	}
	return Percentage(wholePart*10 + fracPart), nil
}

// Pct renders the permille value as a percentage string.
func (p Percentage) Pct() string {
	if p%10 == 0 {
		return fmt.Sprintf("%d%%", int64(p)/10)
	}
	return fmt.Sprintf("%d,%d%%", int64(p)/10, int64(p)%10)
}

// UnitFamily is the basis of a unit price. Values in different families
// are never compared against each other.
type UnitFamily int

// Supported unit-price families. UnitAny is the unqualified query form
// and compares the numeric value in whatever basis the offer carries.
const (
	UnitAny    UnitFamily = iota
	UnitMass              // per kilogram
	UnitVolume            // per liter
	UnitPiece             // per piece
)

// String returns the canonical family spelling used in queries.
func (f UnitFamily) String() string {
	switch f {
	case UnitMass:
		return "kg"
	case UnitVolume:
		return "l"
	case UnitPiece:
		return "pc"
	}
	return "any"
}

// ParseUnitFamily maps a query family token to a family.
func ParseUnitFamily(s string) (UnitFamily, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "kg", "kilo", "kilogram":
		return UnitMass, true
	case "l", "liter", "ltr":
		return UnitVolume, true
	case "pc", "stk", "piece", "enhed":
		return UnitPiece, true
	}
	return UnitAny, false
}

// UnitPrice is a normalized unit price: øre per basis unit (kilogram,
// liter or piece). Upstream values like "8 kr/100g" or "80 kr/kg" must be
// normalized to the basis before comparison; the engine never converts
// between families.
type UnitPrice struct {
	// OrePerBasis is the price in øre per basis unit.
	OrePerBasis int64
	// Family is the basis the value is expressed in.
	Family UnitFamily
}

// allDigits reports whether s is non-empty and consists only of ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
