package restore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/graph"
)

// Validates: PV-177
func TestGraphRecurrence(t *testing.T) {
	start := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) // a Friday
	noEnd := graph.RecurrenceRange{Type: "noEnd", StartDate: "2026-10-02"}

	cases := map[string]struct {
		rule string
		want graph.Recurrence
	}{
		"weekly from the start day": {"FREQ=WEEKLY", graph.Recurrence{
			Pattern: graph.RecurrencePattern{Type: "weekly", Interval: 1, DaysOfWeek: []string{"friday"}}, Range: noEnd,
		}},
		"weekly on days, count": {"FREQ=WEEKLY;BYDAY=MO,WE;WKST=MO;COUNT=10;INTERVAL=2", graph.Recurrence{
			Pattern: graph.RecurrencePattern{Type: "weekly", Interval: 2, DaysOfWeek: []string{"monday", "wednesday"}, FirstDayOfWeek: "monday"},
			Range:   graph.RecurrenceRange{Type: "numbered", StartDate: "2026-10-02", NumberOfOccurrences: 10},
		}},
		"second Tuesday until": {"FREQ=MONTHLY;BYDAY=2TU;UNTIL=20270101T000000Z", graph.Recurrence{
			Pattern: graph.RecurrencePattern{Type: "relativeMonthly", Interval: 1, DaysOfWeek: []string{"tuesday"}, Index: "second"},
			Range:   graph.RecurrenceRange{Type: "endDate", StartDate: "2026-10-02", EndDate: "2027-01-01"},
		}},
		"last Friday by setpos": {"FREQ=MONTHLY;BYDAY=FR;BYSETPOS=-1", graph.Recurrence{
			Pattern: graph.RecurrencePattern{Type: "relativeMonthly", Interval: 1, DaysOfWeek: []string{"friday"}, Index: "last"},
			Range:   noEnd,
		}},
		"monthly on a day": {"FREQ=MONTHLY;BYMONTHDAY=15", graph.Recurrence{
			Pattern: graph.RecurrencePattern{Type: "absoluteMonthly", Interval: 1, DayOfMonth: 15}, Range: noEnd,
		}},
		"yearly from the start date": {"FREQ=YEARLY", graph.Recurrence{
			Pattern: graph.RecurrencePattern{Type: "absoluteYearly", Interval: 1, DayOfMonth: 2, Month: 10}, Range: noEnd,
		}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := graphRecurrence(tc.rule, start)
			assert.True(t, ok)
			assert.Equal(t, &tc.want, got)
		})
	}

	for _, rule := range []string{
		"FREQ=HOURLY", "FREQ=DAILY;BYHOUR=9", "FREQ=MONTHLY;BYDAY=MO,2TU", "FREQ=WEEKLY;BYDAY=5MO", "FREQ=MONTHLY;BYDAY=2TU;BYSETPOS=1",
		"FREQ=DAILY;BYMONTH=12", "FREQ=WEEKLY;BYMONTHDAY=1", "FREQ=MONTHLY;BYMONTH=6",
	} {
		_, ok := graphRecurrence(rule, start)
		assert.False(t, ok, rule)
	}
}

func TestGraphRecurrence_UTCUntilEndsOnTheLocalDate(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	got, ok := graphRecurrence("FREQ=DAILY;UNTIL=20261102T055959Z", time.Date(2026, 10, 25, 22, 0, 0, 0, la))
	require.True(t, ok)
	assert.Equal(t, "2026-11-01", got.Range.EndDate)
}
