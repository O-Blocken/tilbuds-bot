package discordbot

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func notificationTimeReached(value string, now time.Time) (bool, string, error) {
	hour, minute, err := parseNotificationTime(value)
	if err != nil {
		return false, "", err
	}

	target := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	return !now.Before(target), fmt.Sprintf("%02d:%02d", hour, minute), nil
}

func parseNotificationTime(value string) (int, int, error) {
	hourText, minuteText, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return 0, 0, fmt.Errorf("notification time must be HH:MM")
	}

	hour, err := strconv.Atoi(hourText)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid notification hour %q", hourText)
	}
	minute, err := strconv.Atoi(minuteText)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid notification minute %q", minuteText)
	}
	if hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("notification hour must be between 0 and 23")
	}
	if minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("notification minute must be between 0 and 59")
	}
	return hour, minute, nil
}
