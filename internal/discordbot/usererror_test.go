package discordbot

import (
	"errors"
	"strings"
	"testing"

	"spar-tilbuds-bot/internal/search"
)

// TestUserErrorPassesUserFacingErrors keeps actionable messages intact.
func TestUserErrorPassesUserFacingErrors(t *testing.T) {
	bot := &DiscordBot{logger: discardLogger()}

	parseErr := &search.ParseError{Code: search.ErrUnclosedQuote}
	got := bot.userError("Fejl under søgning", parseErr)
	if got != parseErr.UserMessage() {
		t.Errorf("userError(parse error) = %q, want the user message %q", got, parseErr.UserMessage())
	}

	got = bot.userError("Fejl under posting", errPublishBusy)
	if got != errPublishBusy.Error() {
		t.Errorf("userError(busy) = %q, want %q", got, errPublishBusy.Error())
	}

	got = bot.userError("Konceptet kan ikke gemmes", userFacing(errors.New("ukendt reference \"cola\"")))
	if !strings.Contains(got, "ukendt reference") {
		t.Errorf("userError(wrapped user-facing error) = %q, want the wrapped message", got)
	}
}

// TestUserErrorHidesInternalDetails is the regression test for raw error text
// (SQL, file paths, upstream URLs) leaking into a Discord channel.
func TestUserErrorHidesInternalDetails(t *testing.T) {
	logger, logs := captureLogger(t)
	bot := &DiscordBot{logger: logger}
	internal := errors.New("sqlite: disk I/O error: /data/bot.db-wal locked by /data")

	got := bot.userError("Kunne ikke læse ønsker", internal)
	if strings.Contains(got, "sqlite") || strings.Contains(got, "/data/") {
		t.Fatalf("internal detail leaked: %q", got)
	}
	// The detail must still reach the operator's log, or the bot becomes
	// undebuggable.
	if !strings.Contains(logs.String(), "sqlite") {
		t.Errorf("internal detail missing from the log: %q", logs.String())
	}
	if !strings.HasPrefix(got, "Kunne ikke læse ønsker") {
		t.Errorf("message lost its context: %q", got)
	}
	if !strings.Contains(got, "se bottens log") {
		t.Errorf("message should point at the log: %q", got)
	}
}

// TestUserErrorNilKeepsContext covers the defensive nil case.
func TestUserErrorNilKeepsContext(t *testing.T) {
	bot := &DiscordBot{logger: discardLogger()}
	if got := bot.userError("Kunne ikke gemme", nil); got != "Kunne ikke gemme" {
		t.Errorf("userError(nil) = %q", got)
	}
}
