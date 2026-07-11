package calendartrigger

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Rule is Scenery's deterministic RFC 5545-style calendar recurrence subset.
// It intentionally excludes DTSTART, COUNT, UNTIL, seconds, and positional
// BYDAY rules; schedules own timezone and lifecycle separately.
type Rule struct {
	Frequency string
	Interval  int
	Hour      int
	Minute    int
	Weekdays  map[time.Weekday]bool
	MonthDay  int
	Month     time.Month
}

func Parse(source string) (Rule, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return Rule{}, fmt.Errorf("calendar rule is empty")
	}
	values := map[string]string{}
	for _, component := range strings.Split(source, ";") {
		key, value, ok := strings.Cut(component, "=")
		if !ok || key == "" || value == "" || key != strings.ToUpper(key) || values[key] != "" {
			return Rule{}, fmt.Errorf("calendar rule contains an invalid or duplicate component")
		}
		values[key] = value
	}
	rule := Rule{Frequency: values["FREQ"], Interval: 1, Hour: 0, Minute: 0, MonthDay: 1, Month: time.January}
	delete(values, "FREQ")
	switch rule.Frequency {
	case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
	default:
		return Rule{}, fmt.Errorf("calendar FREQ must be DAILY, WEEKLY, MONTHLY, or YEARLY")
	}
	var err error
	if value := values["INTERVAL"]; value != "" {
		rule.Interval, err = boundedInteger(value, 1, 366)
		if err != nil {
			return Rule{}, fmt.Errorf("calendar INTERVAL: %w", err)
		}
	}
	delete(values, "INTERVAL")
	if value := values["BYHOUR"]; value != "" {
		rule.Hour, err = boundedInteger(value, 0, 23)
		if err != nil {
			return Rule{}, fmt.Errorf("calendar BYHOUR: %w", err)
		}
	}
	delete(values, "BYHOUR")
	if value := values["BYMINUTE"]; value != "" {
		rule.Minute, err = boundedInteger(value, 0, 59)
		if err != nil {
			return Rule{}, fmt.Errorf("calendar BYMINUTE: %w", err)
		}
	}
	delete(values, "BYMINUTE")
	if value := values["BYDAY"]; value != "" {
		if rule.Frequency != "WEEKLY" {
			return Rule{}, fmt.Errorf("calendar BYDAY is supported only with WEEKLY")
		}
		rule.Weekdays = map[time.Weekday]bool{}
		for _, item := range strings.Split(value, ",") {
			weekday, ok := weekdays[item]
			if !ok || rule.Weekdays[weekday] {
				return Rule{}, fmt.Errorf("calendar BYDAY contains an invalid or duplicate weekday")
			}
			rule.Weekdays[weekday] = true
		}
	}
	delete(values, "BYDAY")
	if value := values["BYMONTHDAY"]; value != "" {
		if rule.Frequency != "MONTHLY" && rule.Frequency != "YEARLY" {
			return Rule{}, fmt.Errorf("calendar BYMONTHDAY requires MONTHLY or YEARLY")
		}
		rule.MonthDay, err = boundedInteger(value, 1, 31)
		if err != nil {
			return Rule{}, fmt.Errorf("calendar BYMONTHDAY: %w", err)
		}
	}
	delete(values, "BYMONTHDAY")
	if value := values["BYMONTH"]; value != "" {
		if rule.Frequency != "YEARLY" {
			return Rule{}, fmt.Errorf("calendar BYMONTH requires YEARLY")
		}
		month, parseErr := boundedInteger(value, 1, 12)
		if parseErr != nil {
			return Rule{}, fmt.Errorf("calendar BYMONTH: %w", parseErr)
		}
		rule.Month = time.Month(month)
	}
	delete(values, "BYMONTH")
	if len(values) != 0 {
		for key := range values {
			return Rule{}, fmt.Errorf("calendar component %s is not supported", key)
		}
	}
	if rule.Frequency == "WEEKLY" && len(rule.Weekdays) == 0 {
		rule.Weekdays = map[time.Weekday]bool{time.Monday: true}
	}
	return rule, nil
}

func (rule Rule) Next(after time.Time, location *time.Location) time.Time {
	if location == nil {
		location = time.UTC
	}
	next := after.In(location).Truncate(time.Minute).Add(time.Minute)
	deadline := next.Add(5 * 366 * 24 * time.Hour)
	for !next.After(deadline) {
		if rule.matches(next) {
			return next.UTC()
		}
		next = next.Add(time.Minute)
	}
	return time.Time{}
}

func (rule Rule) matches(value time.Time) bool {
	if value.Hour() != rule.Hour || value.Minute() != rule.Minute {
		return false
	}
	switch rule.Frequency {
	case "DAILY":
		return positiveModulo(calendarDay(value)-calendarDay(time.Date(1970, 1, 1, 0, 0, 0, 0, value.Location())), rule.Interval) == 0
	case "WEEKLY":
		anchor := time.Date(1970, 1, 5, 0, 0, 0, 0, value.Location())
		weeks := (calendarDay(value) - calendarDay(anchor)) / 7
		return positiveModulo(weeks, rule.Interval) == 0 && rule.Weekdays[value.Weekday()]
	case "MONTHLY":
		months := (value.Year()-1970)*12 + int(value.Month()) - 1
		return positiveModulo(months, rule.Interval) == 0 && value.Day() == rule.MonthDay
	case "YEARLY":
		return positiveModulo(value.Year()-1970, rule.Interval) == 0 && value.Month() == rule.Month && value.Day() == rule.MonthDay
	default:
		return false
	}
}

func boundedInteger(value string, minimum, maximum int) (int, error) {
	if value != "0" && strings.HasPrefix(value, "0") {
		return 0, fmt.Errorf("must use canonical decimal notation")
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("must be between %d and %d", minimum, maximum)
	}
	return parsed, nil
}

func calendarDay(value time.Time) int {
	return int(time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC).Unix() / int64(24*time.Hour/time.Second))
}

func positiveModulo(value, modulus int) int {
	result := value % modulus
	if result < 0 {
		result += modulus
	}
	return result
}

var weekdays = map[string]time.Weekday{
	"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday,
	"TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday,
}
