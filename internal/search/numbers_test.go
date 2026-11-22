package search

import "testing"

func TestParseMoney(t *testing.T) {
	tests := []struct {
		in      string
		want    Money
		wantErr bool
	}{
		{"49", 4900, false},
		{"49,95", 4995, false},
		{"49.95", 4995, false},
		{"0", 0, false},
		{"0,5", 50, false},
		{"1.234,56", 0, true}, // thousands separators are not supported
		{"49,999", 0, true},   // more than two decimals
		{"", 0, true},
		{"abc", 0, true},
		{",95", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseMoney(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("ParseMoney(%q) expected error, got %v", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseMoney(%q) error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParseMoney(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestMoneyKr(t *testing.T) {
	if got := Money(4995).Kr(); got != "49,95 kr" {
		t.Fatalf("Kr() = %q", got)
	}
	if got := Money(5000).Kr(); got != "50,00 kr" {
		t.Fatalf("Kr() = %q", got)
	}
}

func TestParsePercentage(t *testing.T) {
	tests := []struct {
		in      string
		want    Percentage
		wantErr bool
	}{
		{"25%", 250, false},
		{"25", 250, false},
		{"25,5%", 255, false},
		{"25.5%", 255, false},
		{"0%", 0, false},
		{"100%", 1000, false},
		{"101%", 0, true},
		{"25,55%", 0, true}, // more than one decimal
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := ParsePercentage(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("ParsePercentage(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParsePercentage(%q) error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParsePercentage(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestOpHolds(t *testing.T) {
	tests := []struct {
		op   Op
		a, b int64
		want bool
	}{
		{OpLT, 49, 50, true},
		{OpLT, 50, 50, false},
		{OpLE, 50, 50, true},
		{OpLE, 51, 50, false},
		{OpEQ, 50, 50, true},
		{OpEQ, 49, 50, false},
		{OpGE, 50, 50, true},
		{OpGE, 49, 50, false},
		{OpGT, 51, 50, true},
		{OpGT, 50, 50, false},
	}
	for _, tt := range tests {
		if got := tt.op.Holds(tt.a, tt.b); got != tt.want {
			t.Fatalf("(%d %s %d) = %v, want %v", tt.a, tt.op, tt.b, got, tt.want)
		}
	}
}

func TestSplitOp(t *testing.T) {
	tests := []struct {
		in       string
		wantOp   Op
		wantRest string
	}{
		{"<50", OpLT, "50"},
		{"<=50", OpLE, "50"},
		{"=50", OpEQ, "50"},
		{">=50", OpGE, "50"},
		{">50", OpGT, "50"},
		{"50", OpEQ, "50"},
	}
	for _, tt := range tests {
		op, rest := splitOp(tt.in)
		if op != tt.wantOp || rest != tt.wantRest {
			t.Fatalf("splitOp(%q) = (%v, %q), want (%v, %q)", tt.in, op, rest, tt.wantOp, tt.wantRest)
		}
	}
}

func TestParseUnitFamily(t *testing.T) {
	if f, ok := ParseUnitFamily("kg"); !ok || f != UnitMass {
		t.Fatal("kg should be UnitMass")
	}
	if f, ok := ParseUnitFamily("liter"); !ok || f != UnitVolume {
		t.Fatal("liter should be UnitVolume")
	}
	if f, ok := ParseUnitFamily("stk"); !ok || f != UnitPiece {
		t.Fatal("stk should be UnitPiece")
	}
	if _, ok := ParseUnitFamily("gallon"); ok {
		t.Fatal("unknown family should fail")
	}
}

// TestParseMoneyRejectsOverflow covers the øre conversion: a value that would
// wrap int64 must be an error, not a negative price.
func TestParseMoneyRejectsOverflow(t *testing.T) {
	if _, err := ParseMoney("92233720368547758,07"); err == nil {
		t.Fatal("ParseMoney() accepted an amount that overflows int64 øre")
	}
	if _, err := ParseMoney("92233720368547759"); err == nil {
		t.Fatal("ParseMoney() accepted an amount that overflows int64 øre")
	}
	// The largest safe amount is (MaxInt64-99)/100 kroner; it still parses.
	max := "92233720368547757"
	if _, err := ParseMoney(max); err != nil {
		t.Fatalf("ParseMoney(%q) error = %v", max, err)
	}
	if _, err := ParseMoney("49,95"); err != nil {
		t.Fatalf("ParseMoney(49,95) error = %v", err)
	}
}
