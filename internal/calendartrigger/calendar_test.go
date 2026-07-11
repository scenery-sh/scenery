package calendartrigger

import (
	"testing"
	"time"
)

func TestCalendarRulesAreStrictAndTimezoneAware(t *testing.T) {
	rule, err := Parse("FREQ=WEEKLY;BYDAY=MO,FR;BYHOUR=2;BYMINUTE=15")
	if err != nil {
		t.Fatal(err)
	}
	zone, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 7, 10, 1, 0, 0, 0, zone)
	want := time.Date(2026, 7, 10, 2, 15, 0, 0, zone).UTC()
	if got := rule.Next(after, zone); !got.Equal(want) {
		t.Fatalf("Next() = %s, want %s", got, want)
	}
	for _, invalid := range []string{"business_days", "FREQ=HOURLY", "FREQ=DAILY;BYDAY=MO", "FREQ=DAILY;INTERVAL=01"} {
		if _, err := Parse(invalid); err == nil {
			t.Fatalf("Parse(%q) succeeded", invalid)
		}
	}
}
