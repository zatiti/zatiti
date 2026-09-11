package scheduling

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	// The IANA timezone database is embedded so five-field expressions
	// resolve identically on hosts that ship no /usr/share/zoneinfo.
	_ "time/tzdata"
)

// Five-field cron evaluation.
//
// Fields are minute, hour, day-of-month, month and day-of-week in local
// schedule time. When both day fields are restricted they combine with OR
// semantics (standard vixie-cron); a restricted field with the other left as
// `*` alone decides. Names jan..dec and sun..sat are accepted for the month
// and day-of-week fields, day-of-week 7 means Sunday like 0, and ranges,
// steps and comma lists follow standard cron syntax.
//
// Daylight saving is resolved by UTC occurrence identity: for a repeated
// local wall time each distinct UTC instant is a separate occurrence and is
// admitted once; a local wall time that spring-forward skips never matches.
// Resolution probes the location's offsets around the candidate wall time
// and keeps every instant whose local rendering equals the requested wall
// clock, which yields zero instants in a gap and two across a fall-back
// repeat.

// cronHorizonYears bounds the local-day search for the next occurrence. A
// day is visited only when its month field can match, so the bound costs at
// most a few thousand cheap day checks.
const cronHorizonYears = 4

// cronMaxInstants caps the occurrence enumeration for one admission and
// bounds the past-window scan against unbounded work.
const cronMaxInstants = 1000

// cronMaxScanDays bounds the calendar-day walk behind instantsBetween so a
// pathological window (a wake pending for years) cannot scan unbounded
// history. Occurrences beyond the scan are simply not enumerated.
const cronMaxScanDays = 3660

type cronExpr struct {
	minutes [60]bool
	hours   [24]bool
	days    [32]bool // index 1..31
	months  [13]bool // index 1..12
	dows    [7]bool  // 0 = Sunday

	domRestricted bool
	dowRestricted bool
}

// fieldSpec is the value domain of one cron field.
type fieldRange struct {
	min, max int
	names    map[string]int
}

var (
	minuteField = fieldRange{min: 0, max: 59}
	hourField   = fieldRange{min: 0, max: 23}
	domField    = fieldRange{min: 1, max: 31}
	monthField  = fieldRange{min: 1, max: 12, names: map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}}
	dowField = fieldRange{min: 0, max: 7, names: map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}}
)

// parseCron parses a five-field cron expression. Whitespace separates the
// fields; every field must parse and every named value must resolve.
func parseCron(expression string) (*cronExpr, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return nil, fmt.Errorf("expression must have exactly 5 fields (minute hour day-of-month month day-of-week), got %d", len(fields))
	}
	c := &cronExpr{}
	domRestricted := func() { c.domRestricted = true }
	dowRestricted := func() { c.dowRestricted = true }
	// Day-of-week 7 aliases 0 (Sunday), including inside ranges.
	dowSet := func(v int) {
		if v == 7 {
			v = 0
		}
		c.dows[v] = true
	}
	steps := []struct {
		spec  fieldRange
		field string
		set   func(int)
		onAny func()
		name  string
	}{
		{minuteField, fields[0], func(v int) { c.minutes[v] = true }, nil, "minute"},
		{hourField, fields[1], func(v int) { c.hours[v] = true }, nil, "hour"},
		{domField, fields[2], func(v int) { c.days[v] = true }, domRestricted, "day-of-month"},
		{monthField, fields[3], func(v int) { c.months[v] = true }, nil, "month"},
		{dowField, fields[4], dowSet, dowRestricted, "day-of-week"},
	}
	for _, f := range steps {
		if err := parseField(f.spec, f.field, f.set, f.onAny); err != nil {
			return nil, fmt.Errorf("%s field: %w", f.name, err)
		}
	}
	return c, nil
}

// parseField parses one cron field, calling set for every admitted value and
// onAny when the field is a bare wildcard. Syntax: `*`, lists, ranges a-b,
// steps `*/s`, `a-b/s` and `n/s` (which runs n through the field maximum).
func parseField(spec fieldRange, field string, set func(int), onAny func()) error {
	if field == "*" {
		for v := spec.min; v <= spec.max; v++ {
			set(v)
		}
		if onAny != nil {
			onAny()
		}
		return nil
	}
	for _, part := range strings.Split(field, ",") {
		body, step, hasStep := part, 1, false
		if i := strings.Index(part, "/"); i >= 0 {
			body = part[:i]
			stepStr := part[i+1:]
			s, err := strconv.Atoi(stepStr)
			if err != nil || s < 1 {
				return fmt.Errorf("step %q in %q must be a positive integer", stepStr, part)
			}
			step, hasStep = s, true
		}
		lo, hi := spec.min, spec.max
		switch {
		case body == "*":
			// Full range already set above.
		case strings.HasPrefix(body, "-") || strings.HasSuffix(body, "-"):
			return fmt.Errorf("range %q is malformed", body)
		case strings.Contains(body, "-"):
			bounds := strings.SplitN(body, "-", 2)
			var err error
			if lo, err = resolveValue(bounds[0], spec); err != nil {
				return fmt.Errorf("range %q: %w", body, err)
			}
			if hi, err = resolveValue(bounds[1], spec); err != nil {
				return fmt.Errorf("range %q: %w", body, err)
			}
			if lo > hi {
				return fmt.Errorf("range %q descends", body)
			}
		default:
			v, err := resolveValue(body, spec)
			if err != nil {
				return fmt.Errorf("%q: %w", part, err)
			}
			lo = v
			if hasStep {
				hi = spec.max // n/step runs n through the maximum
			}
		}
		if lo < spec.min || hi > spec.max {
			return fmt.Errorf("values %d-%d fall outside %d-%d", lo, hi, spec.min, spec.max)
		}
		for v := lo; v <= hi; v += step {
			set(v)
		}
	}
	return nil
}

// resolveValue resolves one field token: a number inside the field range or
// a case-insensitive name.
func resolveValue(token string, spec fieldRange) (int, error) {
	if spec.names != nil {
		if v, ok := spec.names[strings.ToLower(token)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(token)
	if err != nil {
		return 0, fmt.Errorf("value %q is not a number or recognized name", token)
	}
	if v < spec.min || v > spec.max {
		return 0, fmt.Errorf("value %d is outside %d-%d", v, spec.min, spec.max)
	}
	return v, nil
}

// resolveInstants resolves one wall clock in loc to every UTC instant that
// renders to it, sorted ascending. An empty result means the wall clock does
// not exist (spring-forward gap); two instants mean a DST fall-back repeat.
func resolveInstants(loc *time.Location, y, mo, d, hh, mm int) []time.Time {
	base := time.Date(y, time.Month(mo), d, hh, mm, 0, 0, time.UTC)
	var out []time.Time
	seen := make(map[int64]bool, 3)
	for _, probe := range []time.Duration{-26 * time.Hour, 0, 26 * time.Hour} {
		_, offset := base.Add(probe).In(loc).Zone()
		cand := base.Add(-time.Duration(offset) * time.Second)
		wall := cand.In(loc)
		if wall.Year() != y || int(wall.Month()) != mo || wall.Day() != d ||
			wall.Hour() != hh || wall.Minute() != mm {
			continue // the requested wall clock does not exist here
		}
		if _, realOffset := cand.In(loc).Zone(); realOffset != offset {
			continue // the probed offset no longer holds at the instant
		}
		key := cand.Unix()
		if !seen[key] {
			seen[key] = true
			out = append(out, cand)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// dayMatches reports whether the cron day fields admit the local date
// (y, mo, d) whose weekday is dow. Both day fields restricted combine with
// OR semantics; a single restricted field applies alone.
func (c *cronExpr) dayMatches(mo, d int, dow time.Weekday) bool {
	switch {
	case c.domRestricted && c.dowRestricted:
		return c.days[d] || c.dows[int(dow)]
	case c.domRestricted:
		return c.days[d]
	case c.dowRestricted:
		return c.dows[int(dow)]
	default:
		return true
	}
}

// instantsForDay returns every cron instant on the local date, sorted. The
// weekday is computed from noon local time, which always exists and stays on
// the calendar date.
func (c *cronExpr) instantsForDay(loc *time.Location, y, mo, d int) []time.Time {
	if !c.months[mo] {
		return nil
	}
	dow := time.Date(y, time.Month(mo), d, 12, 0, 0, 0, loc).Weekday()
	if !c.dayMatches(mo, d, dow) {
		return nil
	}
	var out []time.Time
	for hh := 0; hh < 24; hh++ {
		if !c.hours[hh] {
			continue
		}
		for mm := 0; mm < 60; mm++ {
			if !c.minutes[mm] {
				continue
			}
			out = append(out, resolveInstants(loc, y, mo, d, hh, mm)...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// nextAfter returns the earliest cron instant strictly after after, or false
// when none exists within the bounded horizon.
func (c *cronExpr) nextAfter(after time.Time, loc *time.Location) (time.Time, bool) {
	y, mo, d := after.In(loc).Date()
	for _, t := range c.instantsForDay(loc, y, int(mo), d) {
		if t.After(after) {
			return t, true
		}
	}
	cy, cm, cd := y, int(mo), d
	for i := 0; i < cronHorizonYears*366+2; i++ {
		cy, cm, cd = nextDate(cy, cm, cd)
		if instants := c.instantsForDay(loc, cy, cm, cd); len(instants) > 0 {
			return instants[0], true
		}
	}
	return time.Time{}, false
}

// instantsBetween returns every cron instant in [from, to], ascending,
// capped at cronMaxInstants entries and bounded to cronMaxScanDays of
// calendar walking. Occurrence identity comes from these UTC instants, so a
// DST fall-back repeat contributes both distinct instants and a
// spring-forward gap contributes none.
func (c *cronExpr) instantsBetween(from, to time.Time, loc *time.Location) []time.Time {
	if to.Before(from) {
		return nil
	}
	fy, fm, fd := from.In(loc).Date()
	ty, tm, td := to.In(loc).Date()
	y, mo, d := fy, int(fm), fd
	var out []time.Time
	for {
		for _, t := range c.instantsForDay(loc, y, mo, d) {
			if t.Before(from) || t.After(to) {
				continue
			}
			out = append(out, t)
			if len(out) >= cronMaxInstants {
				return out
			}
		}
		if y == ty && mo == int(tm) && d == td {
			return out
		}
		y, mo, d = nextDate(y, mo, d)
		days := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC).Sub(
			time.Date(fy, fm, fd, 0, 0, 0, 0, time.UTC)) / (24 * time.Hour)
		if days > cronMaxScanDays {
			return out
		}
	}
}

// nextDate advances a calendar date by one day.
func nextDate(y, mo, d int) (int, int, int) {
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	return t.Year(), int(t.Month()), t.Day()
}

// loadLocation resolves a schedule timezone name. "Local" is refused: a
// durable schedule must not depend on host-local settings.
func loadLocation(name string) (*time.Location, error) {
	if name == "" || strings.EqualFold(name, "local") {
		return nil, fmt.Errorf("timezone %q is not an explicit IANA zone", name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("timezone %q is not a known IANA zone", name)
	}
	return loc, nil
}
