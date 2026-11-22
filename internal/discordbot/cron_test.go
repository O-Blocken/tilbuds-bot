package discordbot

import (
	"testing"
	"time"
)

func TestNextCronDelayUsesFridayThirtyMinuteDefault(t *testing.T) {
	now := time.Date(2026, 6, 17, 7, 41, 0, 0, time.Local)

	delay, err := nextCronDelay("*/30 * * * 5", now)
	if err != nil {
		t.Fatalf("nextCronDelay() error = %v", err)
	}

	got := now.Add(delay)
	want := time.Date(2026, 6, 19, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("next run = %s, want %s", got, want)
	}
}

func TestNextCronDelaySupportsSundaySeven(t *testing.T) {
	now := time.Date(2026, 6, 20, 23, 59, 0, 0, time.Local)

	delay, err := nextCronDelay("0 0 * * 7", now)
	if err != nil {
		t.Fatalf("nextCronDelay() error = %v", err)
	}

	got := now.Add(delay)
	want := time.Date(2026, 6, 21, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("next run = %s, want %s", got, want)
	}
}

// TestCronDayOfMonthOrDayOfWeek covers classic cron semantics: when both date
// fields are constrained, either one matching is enough.
func TestCronDayOfMonthOrDayOfWeek(t *testing.T) {
	schedule, err := parseCron("0 12 1 * 1")
	if err != nil {
		t.Fatalf("parseCron() error = %v", err)
	}

	// 2026-06-01 is a Monday and the 1st: matches both fields.
	if !schedule.matches(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)) {
		t.Error("the 1st that is also a Monday must match")
	}
	// 2026-06-08 is a Monday but not the 1st: matches through day-of-week.
	if !schedule.matches(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)) {
		t.Error("a Monday that is not the 1st must match")
	}
	// 2026-07-01 is the 1st but a Wednesday: matches through day-of-month.
	if !schedule.matches(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)) {
		t.Error("the 1st that is not a Monday must match")
	}
	// 2026-06-10 is a Wednesday and not the 1st.
	if schedule.matches(time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)) {
		t.Error("neither field matches, so the day must not match")
	}
}

// TestCronOneDateFieldRestricted keeps the AND behaviour when only one date
// field is constrained.
func TestCronOneDateFieldRestricted(t *testing.T) {
	// Every 30 minutes on Fridays: the default schedule.
	schedule, err := parseCron("*/30 * * * 5")
	if err != nil {
		t.Fatalf("parseCron() error = %v", err)
	}
	// 2026-06-12 is a Friday.
	if !schedule.matches(time.Date(2026, 6, 12, 8, 30, 0, 0, time.UTC)) {
		t.Error("Friday 08:30 must match the default schedule")
	}
	// 2026-06-11 is a Thursday.
	if schedule.matches(time.Date(2026, 6, 11, 8, 30, 0, 0, time.UTC)) {
		t.Error("Thursday must not match a Friday-only schedule")
	}

	everyDay, err := parseCron("0 7 * * *")
	if err != nil {
		t.Fatalf("parseCron() error = %v", err)
	}
	if !everyDay.matches(time.Date(2026, 6, 11, 7, 0, 0, 0, time.UTC)) {
		t.Error("an all-wildcard date must match every day")
	}
}
