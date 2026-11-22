package discordbot

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cronSchedule struct {
	minute     map[int]bool
	hour       map[int]bool
	dayOfMonth map[int]bool
	month      map[int]bool
	dayOfWeek  map[int]bool
	// dateRestricted records whether day-of-month/day-of-week were actually
	// constrained, which selects between the classic OR and AND behaviour.
	dateRestricted bool
	weekRestricted bool
}

func nextCronDelay(expression string, now time.Time) (time.Duration, error) {
	schedule, err := parseCron(expression)
	if err != nil {
		return 0, err
	}
	next := now.Truncate(time.Minute).Add(time.Minute)
	limit := next.AddDate(1, 0, 0)

	for next.Before(limit) {
		if schedule.matches(next) {
			return next.Sub(now), nil
		}
		next = next.Add(time.Minute)
	}
	return 0, fmt.Errorf("cron expression %q did not match within one year", expression)
}

func parseCron(expression string) (cronSchedule, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return cronSchedule{}, fmt.Errorf("cron expression must have 5 fields")
	}

	minute, err := parseCronField(fields[0], 0, 59, false)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("minute field: %w", err)
	}
	hour, err := parseCronField(fields[1], 0, 23, false)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("hour field: %w", err)
	}
	dayOfMonth, err := parseCronField(fields[2], 1, 31, false)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("day-of-month field: %w", err)
	}
	month, err := parseCronField(fields[3], 1, 12, false)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("month field: %w", err)
	}
	dayOfWeek, err := parseCronField(fields[4], 0, 7, true)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("day-of-week field: %w", err)
	}

	return cronSchedule{
		minute:         minute,
		hour:           hour,
		dayOfMonth:     dayOfMonth,
		month:          month,
		dayOfWeek:      dayOfWeek,
		dateRestricted: !isCronWildcard(fields[2]),
		weekRestricted: !isCronWildcard(fields[4]),
	}, nil
}

// isCronWildcard reports whether a field starts with '*', which is how cron
// decides that a date field is unconstrained.
func isCronWildcard(field string) bool {
	return strings.HasPrefix(strings.TrimSpace(field), "*")
}

func (s cronSchedule) matches(value time.Time) bool {
	if !s.minute[value.Minute()] || !s.hour[value.Hour()] || !s.month[int(value.Month())] {
		return false
	}
	dayMatches := s.dayOfMonth[value.Day()]
	weekdayMatches := s.dayOfWeek[int(value.Weekday())]
	// Classic cron: when both date fields are constrained, either may match
	// (`0 0 1 * 1` is "the 1st or any Monday"). Otherwise both must match,
	// which makes the unconstrained field a no-op.
	if s.dateRestricted && s.weekRestricted {
		return dayMatches || weekdayMatches
	}
	return dayMatches && weekdayMatches
}

func parseCronField(field string, minValue, maxValue int, sundaySeven bool) (map[int]bool, error) {
	values := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty field part")
		}
		if err := addCronPart(values, part, minValue, maxValue, sundaySeven); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func addCronPart(values map[int]bool, part string, minValue, maxValue int, sundaySeven bool) error {
	rangePart, step := part, 1
	if before, after, ok := strings.Cut(part, "/"); ok {
		rangePart = before
		parsedStep, err := strconv.Atoi(after)
		if err != nil || parsedStep <= 0 {
			return fmt.Errorf("invalid step %q", after)
		}
		step = parsedStep
	}

	start, end, err := cronRange(rangePart, minValue, maxValue)
	if err != nil {
		return err
	}
	for value := start; value <= end; value += step {
		if sundaySeven && value == 7 {
			values[0] = true
		} else {
			values[value] = true
		}
	}
	return nil
}

func cronRange(value string, minValue, maxValue int) (int, int, error) {
	if value == "*" {
		return minValue, maxValue, nil
	}
	if start, end, ok := strings.Cut(value, "-"); ok {
		parsedStart, err := parseCronInt(start, minValue, maxValue)
		if err != nil {
			return 0, 0, err
		}
		parsedEnd, err := parseCronInt(end, minValue, maxValue)
		if err != nil {
			return 0, 0, err
		}
		if parsedStart > parsedEnd {
			return 0, 0, fmt.Errorf("range %q starts after it ends", value)
		}
		return parsedStart, parsedEnd, nil
	}

	parsed, err := parseCronInt(value, minValue, maxValue)
	if err != nil {
		return 0, 0, err
	}
	return parsed, parsed, nil
}

func parseCronInt(value string, minValue, maxValue int) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", value)
	}
	if parsed < minValue || parsed > maxValue {
		return 0, fmt.Errorf("value %d outside %d-%d", parsed, minValue, maxValue)
	}
	return parsed, nil
}
