package discordbot

import (
	"strings"
	"testing"

	"spar-tilbuds-bot/internal/match"

	"tjekapi"
)

func TestShouldPingRoleRequiresConfiguredRoleAndDeals(t *testing.T) {
	tests := []struct {
		name      string
		roleID    int64
		dealCount int
		want      bool
	}{
		{name: "role and deals", roleID: 123, dealCount: 1, want: true},
		{name: "role but no deals", roleID: 123, dealCount: 0, want: false},
		{name: "deals but no role", roleID: 0, dealCount: 1, want: false},
		{name: "negative role", roleID: -1, dealCount: 1, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldPingRole(test.roleID, test.dealCount); got != test.want {
				t.Fatalf("shouldPingRole(%d, %d) = %t, want %t", test.roleID, test.dealCount, got, test.want)
			}
		})
	}
}

func TestBuildDealEmbedIncludesUnitPrice(t *testing.T) {
	unitPrice := 95.0 / 18.0
	price := 95.0
	embed := buildDealEmbed(match.ScoredDeal{
		Deal: tjekapi.Deal{
			CleanName: "Carlsberg",
			Price:     &price,
			UnitPrice: &unitPrice,
		},
		UnitLabel: "12,50 kr/kg",
	})

	if !strings.Contains(embed.Description, "Pris: 95 kr\nPris for 1: 5,28 kr") {
		t.Fatalf("description = %q, want unit price below price", embed.Description)
	}
	if !strings.Contains(embed.Description, "Enhedspris: 12,50 kr/kg") {
		t.Fatalf("description = %q, want enhedspris line", embed.Description)
	}
}

func TestFormatUnitPriceUsesDanishDecimalComma(t *testing.T) {
	tests := []struct {
		price float64
		want  string
	}{
		{price: 5.28, want: "5,28"},
		{price: 5.0, want: "5"},
		{price: 12.5, want: "12,50"},
		{price: 10.61, want: "10,61"},
	}

	for _, test := range tests {
		if got := formatUnitPrice(test.price); got != test.want {
			t.Fatalf("formatUnitPrice(%v) = %q, want %q", test.price, got, test.want)
		}
	}
}
func TestPostDealsEmptyTracksNothing(t *testing.T) {
	bot := &DiscordBot{}
	got, err := bot.postDeals("123", feed{retailer: "spar"}, nil)
	if err != nil {
		t.Fatalf("postDeals() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("postDeals() = %+v, want no tracked messages", got)
	}
}
