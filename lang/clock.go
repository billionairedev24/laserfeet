package lang

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Date is a day on the calendar, with no time of day and no time zone: 2026-11-06.
type Date struct{ t time.Time } // midnight UTC

// Moment is a point in time (the Time type), kept in UTC: 2026-11-06T15:04:05Z.
type Moment struct{ t time.Time }

// Duration is a length of time, to the nanosecond: 1h30m.
type Duration time.Duration

// UUID is a 128-bit identifier: 0b7f3c1e-....
type UUID [16]byte

func (d Date) String() string   { return d.t.Format("2006-01-02") }
func (m Moment) String() string { return m.t.Format(time.RFC3339Nano) }
func (u UUID) String() string {
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// String writes a duration the short way: 90 seconds is 1m30s, two days is 2d.
func (d Duration) String() string {
	if d < 0 {
		return "-" + (-d).String()
	}
	if time.Duration(d) >= day {
		days, rest := time.Duration(d)/day, Duration(time.Duration(d)%day)
		if rest == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%s", days, rest)
	}
	s := time.Duration(d).String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

func parseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return Date{}, fmt.Errorf("%q is not a date; write it like 2026-11-06", s)
	}
	return Date{t}, nil
}

func parseMoment(s string) (Moment, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999Z07", "2006-01-02T15:04:05", "2006-01-02 15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return Moment{t.UTC()}, nil
		}
	}
	return Moment{}, fmt.Errorf("%q is not a time; write it like 2026-11-06T15:04:05Z", s)
}

// parseDuration reads 1h30m, 45s, 2d or 2d6h (d is a day of 24 hours).
func parseDuration(s string) (Duration, error) {
	text := strings.TrimSpace(s)
	var days time.Duration
	if i := strings.Index(text, "d"); i > 0 {
		n, err := strconv.Atoi(strings.TrimPrefix(text[:i], "-"))
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration; write it like 1h30m, 45s or 2d", s)
		}
		days = time.Duration(n) * day
		if strings.HasPrefix(text, "-") {
			days = -days
		}
		text = text[i+1:]
		if text == "" {
			return Duration(days), nil
		}
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration; write it like 1h30m, 45s or 2d", s)
	}
	if days < 0 {
		d = -d
	}
	return Duration(days + d), nil
}

func parseUUID(s string) (UUID, error) {
	var u UUID
	h := strings.ReplaceAll(strings.TrimSpace(s), "-", "")
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 16 {
		return u, fmt.Errorf("%q is not a UUID", s)
	}
	copy(u[:], b)
	return u, nil
}

// newUUID makes a random (version 4) UUID.
func newUUID() UUID {
	var u UUID
	rand.Read(u[:])
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	return u
}

const day = 24 * time.Hour

// clockOp handles arithmetic with dates, times and durations.
func (in *Interp) clockOp(line int, op string, l, r Value) (Value, bool) {
	switch a := l.(type) {
	case Duration:
		switch b := r.(type) {
		case Duration:
			switch op {
			case "+":
				return a + b, true
			case "-":
				return a - b, true
			}
		case int64:
			switch op {
			case "*":
				return a * Duration(b), true
			case "/":
				if b == 0 {
					in.fail(line, "a duration can't be split into zero parts")
				}
				return a / Duration(b), true
			}
		case Moment:
			if op == "+" {
				return Moment{b.t.Add(time.Duration(a))}, true
			}
		}
	case int64:
		if b, ok := r.(Duration); ok && op == "*" {
			return Duration(a) * b, true
		}
	case Moment:
		switch b := r.(type) {
		case Duration:
			switch op {
			case "+":
				return Moment{a.t.Add(time.Duration(b))}, true
			case "-":
				return Moment{a.t.Add(-time.Duration(b))}, true
			}
		case Moment:
			if op == "-" {
				return Duration(a.t.Sub(b.t)), true
			}
		}
	case Date:
		switch b := r.(type) {
		case Duration:
			if time.Duration(b)%day != 0 {
				in.fail(line, "a Date moves by whole days, but %s isn't a whole number of days", b)
			}
			switch op {
			case "+":
				return Date{a.t.Add(time.Duration(b))}, true
			case "-":
				return Date{a.t.Add(-time.Duration(b))}, true
			}
		case Date:
			if op == "-" {
				return Duration(a.t.Sub(b.t)), true
			}
		}
	}
	return nil, false
}

// clockCmp compares two dates, two times or two durations.
func clockCmp(a, b Value) (int, bool) {
	cmp := func(x, y time.Time) int { return x.Compare(y) }
	switch x := a.(type) {
	case Date:
		if y, ok := b.(Date); ok {
			return cmp(x.t, y.t), true
		}
	case Moment:
		if y, ok := b.(Moment); ok {
			return cmp(x.t, y.t), true
		}
	case Duration:
		if y, ok := b.(Duration); ok {
			switch {
			case x < y:
				return -1, true
			case x > y:
				return 1, true
			}
			return 0, true
		}
	}
	return 0, false
}

// clockMember is d.year, t.hour, span.inMinutes and the like.
func (in *Interp) clockMember(v Value, name string) (Value, bool) {
	switch x := v.(type) {
	case Date:
		switch name {
		case "year":
			return int64(x.t.Year()), true
		case "month":
			return int64(x.t.Month()), true
		case "day":
			return int64(x.t.Day()), true
		case "weekday":
			return x.t.Weekday().String(), true
		case "addDays":
			return builtin("addDays", func(in *Interp, line int, a []Value) Value {
				wantArgs(in, line, "addDays", a, 1)
				return Date{x.t.AddDate(0, 0, int(in.intArg(line, a[0], "the number of days")))}
			}), true
		case "addMonths":
			return builtin("addMonths", func(in *Interp, line int, a []Value) Value {
				wantArgs(in, line, "addMonths", a, 1)
				return Date{x.t.AddDate(0, int(in.intArg(line, a[0], "the number of months")), 0)}
			}), true
		}
	case Moment:
		switch name {
		case "date":
			y, m, d := x.t.Date()
			return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}, true
		case "hour":
			return int64(x.t.Hour()), true
		case "minute":
			return int64(x.t.Minute()), true
		case "second":
			return int64(x.t.Second()), true
		}
	case Duration:
		switch name {
		case "inSeconds":
			return int64(time.Duration(x) / time.Second), true
		case "inMinutes":
			return decimalRatio(int64(x), int64(time.Minute)), true
		case "inHours":
			return decimalRatio(int64(x), int64(time.Hour)), true
		case "inDays":
			return decimalRatio(int64(x), int64(day)), true
		}
	}
	return nil, false
}

// decimalRatio is a / b as an exact Decimal (to 28 places).
func decimalRatio(a, b int64) *Decimal {
	q, _ := decimalFromInt(a).Div(decimalFromInt(b))
	return q
}

func (in *Interp) intArg(line int, v Value, what string) int64 {
	n, ok := v.(int64)
	if !ok {
		in.fail(line, "%s must be Int, but got %s", what, typeName(v))
	}
	return n
}

// clockBuiltins are date(...), seconds(n) and the others that make these values.
func (in *Interp) clockBuiltins(def func(*Builtin)) {
	def(builtin("date", func(in *Interp, line int, a []Value) Value {
		switch len(a) {
		case 1:
			switch x := a[0].(type) {
			case Date:
				return x
			case Moment:
				v, _ := in.clockMember(x, "date")
				return v
			}
			d, err := parseDate(Text(a[0]))
			if err != nil {
				in.fail(line, "%v", err)
			}
			return d
		case 3:
			y, m, d := in.intArg(line, a[0], "the year"), in.intArg(line, a[1], "the month"), in.intArg(line, a[2], "the day")
			t := time.Date(int(y), time.Month(m), int(d), 0, 0, 0, 0, time.UTC)
			if t.Year() != int(y) || int64(t.Month()) != m || int64(t.Day()) != d {
				in.fail(line, "%d-%02d-%02d is not a real date", y, m, d)
			}
			return Date{t}
		}
		in.fail(line, "date takes text like \"2026-11-06\", or a year, month and day")
		return nil
	}))
	unit := func(name string, size time.Duration) {
		def(builtin(name, func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, name, a, 1)
			switch x := a[0].(type) {
			case int64:
				return Duration(time.Duration(x) * size)
			case *Decimal:
				f := x.Float64() * float64(size)
				return Duration(time.Duration(f))
			}
			in.fail(line, "%s takes a number, like %s(5)", name, name)
			return nil
		}))
	}
	unit("seconds", time.Second)
	unit("minutes", time.Minute)
	unit("hours", time.Hour)
	unit("days", day)
	def(builtin("duration", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "duration", a, 1)
		if d, ok := a[0].(Duration); ok {
			return d
		}
		d, err := parseDuration(Text(a[0]))
		if err != nil {
			in.fail(line, "%v", err)
		}
		return d
	}))
	def(builtin("uuid", func(in *Interp, line int, a []Value) Value {
		if len(a) == 0 {
			return newUUID()
		}
		wantArgs(in, line, "uuid", a, 1)
		if u, ok := a[0].(UUID); ok {
			return u
		}
		u, err := parseUUID(Text(a[0]))
		if err != nil {
			in.fail(line, "%v", err)
		}
		return u
	}))
}

// clockFrom reads one of these values from outside data (always text in JSON).
func clockFrom(k Kind, v Value) (Value, error) {
	s, ok := v.(string)
	if !ok {
		switch v.(type) {
		case Date, Moment, Duration, UUID:
			return v, nil
		}
		return nil, fmt.Errorf("must be text")
	}
	switch k {
	case KDate:
		return parseDate(s)
	case KTime:
		return parseMoment(s)
	case KDuration:
		return parseDuration(s)
	case KUUID:
		return parseUUID(s)
	}
	return nil, fmt.Errorf("unknown")
}
