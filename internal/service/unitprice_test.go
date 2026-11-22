package service

import (
	"testing"

	"tjekapi"
)

// multipackDeal builds a deal with OCR pack-size fields filled.
func multipackDeal(price float64, quantity, volumeCL int) tjekapi.Deal {
	deal := tjekapi.Deal{
		PackQuantity: quantity,
		PackVolumeCL: volumeCL,
	}
	if price > 0 {
		deal.Price = &price
	}
	return deal
}

func TestFormatUnitLabel(t *testing.T) {
	tests := []struct {
		unit     float64
		baseUnit string
		want     string
	}{
		{12.5, "kilogram", "12,50 kr/kg"},
		{8.05, "kg", "8,05 kr/kg"},
		{3.29, "liter", "3,29 kr/l"},
		{10, "piece", "10,00 kr/stk"},
		{7.5, "", "7,50 kr/stk"},
	}
	for _, tt := range tests {
		if got := formatUnitLabel(tt.unit, tt.baseUnit); got != tt.want {
			t.Fatalf("formatUnitLabel(%v, %q) = %q, want %q", tt.unit, tt.baseUnit, got, tt.want)
		}
	}
}

func TestDerivedPackLabel(t *testing.T) {
	price := 95.0
	// 18 x 33 cl = 5,94 liter → 95/5,94 ≈ 16,00 kr/l
	label := derivedPackLabel(multipackDeal(price, 18, 33))
	if label != "15,99 kr/l" && label != "16,00 kr/l" {
		t.Fatalf("derivedPackLabel(95, 18x33cl) = %q, want ≈16 kr/l", label)
	}

	if got := derivedPackLabel(multipackDeal(price, 0, 33)); got != "" {
		t.Fatalf("no quantity must yield empty label, got %q", got)
	}
	if got := derivedPackLabel(multipackDeal(price, 18, 0)); got != "" {
		t.Fatalf("no volume must yield empty label, got %q", got)
	}
	if got := derivedPackLabel(multipackDeal(0, 18, 33)); got != "" {
		t.Fatalf("no price must yield empty label, got %q", got)
	}
}

func TestUnitLabelSuffix(t *testing.T) {
	if got := UnitLabelSuffix("kilogram"); got != "kr/kg" {
		t.Fatalf("kilogram suffix = %q", got)
	}
	if got := UnitLabelSuffix("liter"); got != "kr/l" {
		t.Fatalf("liter suffix = %q", got)
	}
	if got := UnitLabelSuffix("piece"); got != "kr/stk" {
		t.Fatalf("piece suffix = %q", got)
	}
	if got := UnitLabelSuffix("mystery"); got != "kr/stk" {
		t.Fatalf("unknown suffix = %q, want kr/stk default", got)
	}
}
