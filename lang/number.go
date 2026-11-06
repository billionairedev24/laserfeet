package lang

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Laser Feet has three kinds of number:
//
//	Int      whole numbers, stored as int64; overflow is an error, never a silent wrap
//	Float    fast approximate decimals (IEEE 754 float64), for measurements
//	Decimal  exact decimals, for money: 0.1 + 0.2 is exactly 0.3
//
// A number written with a decimal point (19.99) is a Decimal, so exact is the default.
// Int mixes freely with either; Float and Decimal never mix without a conversion.

// Decimal is an exact decimal number: coefficient × 10^-scale.
// Most decimals (prices, totals, rates) fit in 64 bits and are kept in small,
// which costs no extra memory; only very large ones use big.
type Decimal struct {
	small int64
	big   *big.Int // used instead of small when the coefficient doesn't fit
	scale int32
	// written directly in the code, like 2.5: it may also be used as a Float.
	// Results of calculations are never literal.
	literal bool
}

const maxDecimalScale = 28 // digits kept after the point when a result can't be exact

var (
	bigTen   = big.NewInt(10)
	errRange = errors.New("this number is too big for an Int")
)

var smallPow10 = func() (p [19]int64) {
	p[0] = 1
	for i := 1; i < len(p); i++ {
		p[i] = p[i-1] * 10
	}
	return
}()

func pow10(n int32) *big.Int {
	return new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil)
}

// mkDec stores c compactly when it fits in 64 bits.
func mkDec(c *big.Int, scale int32) *Decimal {
	if c.IsInt64() {
		return &Decimal{small: c.Int64(), scale: scale}
	}
	return &Decimal{big: c, scale: scale}
}

func (d *Decimal) coef() *big.Int {
	if d.big != nil {
		return d.big
	}
	return big.NewInt(d.small)
}

func (d *Decimal) sign() int {
	if d.big != nil {
		return d.big.Sign()
	}
	return cmp3(d.small < 0, d.small > 0)
}

// ParseDecimal reads text like "19.99" or "-3" exactly.
func ParseDecimal(s string) (*Decimal, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "_", ""))
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" && frac == "" {
		return nil, errors.New("not a number")
	}
	digits := whole + frac
	for _, r := range digits {
		if r < '0' || r > '9' {
			return nil, errors.New("not a number")
		}
	}
	if digits == "" {
		digits = "0"
	}
	c, _ := new(big.Int).SetString(digits, 10)
	if neg {
		c.Neg(c)
	}
	return mkDec(c, int32(len(frac))), nil
}

func decimalFromInt(i int64) *Decimal { return &Decimal{small: i} }

func decimalFromFloat(f float64) (*Decimal, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, errors.New("this Float has no exact value")
	}
	return ParseDecimal(strconv.FormatFloat(f, 'f', -1, 64))
}

func (d *Decimal) String() string {
	var s string
	neg := d.sign() < 0
	if d.big != nil {
		s = new(big.Int).Abs(d.big).String()
	} else if d.small == math.MinInt64 {
		s = new(big.Int).Abs(big.NewInt(d.small)).String()
	} else {
		v := d.small
		if v < 0 {
			v = -v
		}
		s = strconv.FormatInt(v, 10)
	}
	if d.scale > 0 {
		if len(s) <= int(d.scale) {
			s = strings.Repeat("0", int(d.scale)-len(s)+1) + s
		}
		s = s[:len(s)-int(d.scale)] + "." + s[len(s)-int(d.scale):]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// alignSmall brings two compact decimals to the same scale, if that fits in 64 bits.
func alignSmall(a, b *Decimal) (int64, int64, int32, bool) {
	if a.big != nil || b.big != nil {
		return 0, 0, 0, false
	}
	x, y := a.small, b.small
	switch {
	case a.scale > b.scale:
		k := a.scale - b.scale
		if k >= int32(len(smallPow10)) {
			return 0, 0, 0, false
		}
		var ok bool
		if y, ok = mulInt(y, smallPow10[k]); !ok {
			return 0, 0, 0, false
		}
		return x, y, a.scale, true
	case b.scale > a.scale:
		k := b.scale - a.scale
		if k >= int32(len(smallPow10)) {
			return 0, 0, 0, false
		}
		var ok bool
		if x, ok = mulInt(x, smallPow10[k]); !ok {
			return 0, 0, 0, false
		}
		return x, y, b.scale, true
	}
	return x, y, a.scale, true
}

// align returns both coefficients at the same scale (slow, general form).
func align(a, b *Decimal) (*big.Int, *big.Int, int32) {
	ac, bc := a.coef(), b.coef()
	switch {
	case a.scale == b.scale:
		return ac, bc, a.scale
	case a.scale > b.scale:
		return ac, new(big.Int).Mul(bc, pow10(a.scale-b.scale)), a.scale
	default:
		return new(big.Int).Mul(ac, pow10(b.scale-a.scale)), bc, b.scale
	}
}

func (a *Decimal) Add(b *Decimal) *Decimal {
	if x, y, s, ok := alignSmall(a, b); ok {
		if c, ok := addInt(x, y); ok {
			return &Decimal{small: c, scale: s}
		}
	}
	x, y, s := align(a, b)
	return mkDec(new(big.Int).Add(x, y), s)
}

func (a *Decimal) Sub(b *Decimal) *Decimal {
	if x, y, s, ok := alignSmall(a, b); ok {
		if c, ok := subInt(x, y); ok {
			return &Decimal{small: c, scale: s}
		}
	}
	x, y, s := align(a, b)
	return mkDec(new(big.Int).Sub(x, y), s)
}

func (a *Decimal) Mul(b *Decimal) *Decimal {
	scale := a.scale + b.scale
	if a.big == nil && b.big == nil && scale <= maxDecimalScale {
		if c, ok := mulInt(a.small, b.small); ok {
			return &Decimal{small: c, scale: scale}
		}
	}
	d := mkDec(new(big.Int).Mul(a.coef(), b.coef()), scale)
	if d.scale > maxDecimalScale {
		return d.Round(maxDecimalScale)
	}
	return d
}

// Div divides, keeping up to 28 digits after the point and removing trailing zeros.
func (a *Decimal) Div(b *Decimal) (*Decimal, error) {
	if b.sign() == 0 {
		return nil, errors.New("you can't divide by zero")
	}
	// a/b = (ca·10^sb) / (cb·10^sa); scale the top by 10^(maxScale+1) and round.
	top := new(big.Int).Mul(a.coef(), pow10(b.scale+maxDecimalScale+1))
	bottom := new(big.Int).Mul(b.coef(), pow10(a.scale))
	q := new(big.Int).Quo(top, bottom)
	d := (&Decimal{big: q, scale: maxDecimalScale + 1}).Round(maxDecimalScale)
	return d.trim(), nil
}

func (a *Decimal) Mod(b *Decimal) (*Decimal, error) {
	if b.sign() == 0 {
		return nil, errors.New("you can't divide by zero")
	}
	if x, y, s, ok := alignSmall(a, b); ok {
		return &Decimal{small: x % y, scale: s}, nil
	}
	x, y, s := align(a, b)
	return mkDec(new(big.Int).Rem(x, y), s), nil
}

func (a *Decimal) Neg() *Decimal {
	if a.big == nil && a.small != math.MinInt64 {
		return &Decimal{small: -a.small, scale: a.scale, literal: a.literal}
	}
	d := mkDec(new(big.Int).Neg(a.coef()), a.scale)
	d.literal = a.literal
	return d
}

func (a *Decimal) Cmp(b *Decimal) int {
	if x, y, _, ok := alignSmall(a, b); ok {
		return cmp3(x < y, x > y)
	}
	x, y, _ := align(a, b)
	return x.Cmp(y)
}

// Round rounds to the given number of places, with halves rounded away from zero
// (2.5 becomes 3, -2.5 becomes -3), the way people round money by hand.
func (d *Decimal) Round(places int32) *Decimal {
	if places < 0 {
		places = 0
	}
	if d.scale <= places {
		return mkDec(new(big.Int).Mul(d.coef(), pow10(places-d.scale)), places)
	}
	c := d.coef()
	div := pow10(d.scale - places)
	q, r := new(big.Int).QuoRem(c, div, new(big.Int))
	twice := new(big.Int).Mul(new(big.Int).Abs(r), big.NewInt(2))
	if twice.Cmp(div) >= 0 {
		if c.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return mkDec(q, places)
}

// trim removes trailing zeros after the point.
func (d *Decimal) trim() *Decimal {
	c, s := new(big.Int).Set(d.coef()), d.scale
	m := new(big.Int)
	for s > 0 {
		q, r := new(big.Int).QuoRem(c, bigTen, m)
		if r.Sign() != 0 {
			break
		}
		c, s = q, s-1
	}
	return mkDec(c, s)
}

func (d *Decimal) IsWhole() bool { return d.trim().scale == 0 }

// Int64 drops anything after the point (toward zero).
func (d *Decimal) Int64() (int64, error) {
	if d.big == nil && d.scale < int32(len(smallPow10)) {
		return d.small / smallPow10[d.scale], nil
	}
	q := new(big.Int).Quo(d.coef(), pow10(d.scale))
	if !q.IsInt64() {
		return 0, errRange
	}
	return q.Int64(), nil
}

func (d *Decimal) Float64() float64 {
	if d.big == nil && d.scale < int32(len(smallPow10)) && d.small > -(1<<53) && d.small < 1<<53 {
		return float64(d.small) / float64(smallPow10[d.scale])
	}
	f, _ := strconv.ParseFloat(d.String(), 64)
	return f
}

// ---------- Int arithmetic that refuses to overflow ----------

func addInt(a, b int64) (int64, bool) {
	c := a + b
	return c, (c > a) == (b > 0)
}

func subInt(a, b int64) (int64, bool) {
	c := a - b
	return c, (c < a) == (b > 0)
}

func mulInt(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	c := a * b
	if (c < 0) != ((a < 0) != (b < 0)) || c/b != a {
		return c, false
	}
	return c, true
}

// ---------- helpers used across the engine ----------

// intCache holds boxed copies of common Ints so counters and indexes don't allocate.
var intCache = func() (c [4096]Value) {
	for i := range c {
		c[i] = int64(i)
	}
	return
}()

func box(i int64) Value {
	if i >= 0 && i < int64(len(intCache)) {
		return intCache[i]
	}
	return i
}

func isNumber(v Value) bool {
	switch v.(type) {
	case int64, float64, *Decimal:
		return true
	}
	return false
}

// numberCmp compares two numbers of any kind by value.
func numberCmp(a, b Value) (int, bool) {
	switch x := a.(type) {
	case int64:
		switch y := b.(type) {
		case int64:
			return cmp3(x < y, x > y), true
		case float64:
			return cmp3(float64(x) < y, float64(x) > y), true
		case *Decimal:
			return decimalFromInt(x).Cmp(y), true
		}
	case float64:
		switch y := b.(type) {
		case int64:
			return cmp3(x < float64(y), x > float64(y)), true
		case float64:
			return cmp3(x < y, x > y), true
		case *Decimal:
			f := y.Float64()
			return cmp3(x < f, x > f), true
		}
	case *Decimal:
		switch y := b.(type) {
		case int64:
			return x.Cmp(decimalFromInt(y)), true
		case float64:
			f := x.Float64()
			return cmp3(f < y, f > y), true
		case *Decimal:
			return x.Cmp(y), true
		}
	}
	return 0, false
}

func cmp3(less, more bool) int {
	switch {
	case less:
		return -1
	case more:
		return 1
	}
	return 0
}

func formatFloat(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatFloat(f, 'f', 1, 64) // 2.0, so a Float still looks like one
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// ---------- arithmetic ----------

// arith applies + - * / % to two numbers following Laser Feet's rules:
// Int with Int stays Int (except /, which gives an exact Decimal),
// Decimal with Int or Decimal gives Decimal, Float with Int or Float gives Float,
// and Float never mixes with Decimal without a conversion.
func (in *Interp) arith(line int, op string, l, r Value) Value {
	switch a := l.(type) {
	case int64:
		switch b := r.(type) {
		case int64:
			return in.intOp(line, op, a, b)
		case float64:
			return in.floatOp(line, op, float64(a), b)
		case *Decimal:
			return in.decOp(line, op, decimalFromInt(a), b)
		}
	case float64:
		switch b := r.(type) {
		case int64:
			return in.floatOp(line, op, a, float64(b))
		case float64:
			return in.floatOp(line, op, a, b)
		case *Decimal:
			if b.literal {
				return in.floatOp(line, op, a, b.Float64())
			}
			in.fail(line, "'%s' can't mix a Float with an exact Decimal. Convert one first: float(x) or decimal(x)", op)
		}
	case *Decimal:
		switch b := r.(type) {
		case int64:
			return in.decOp(line, op, a, decimalFromInt(b))
		case *Decimal:
			return in.decOp(line, op, a, b)
		case float64:
			if a.literal {
				return in.floatOp(line, op, a.Float64(), b)
			}
			in.fail(line, "'%s' can't mix an exact Decimal with a Float. Convert one first: float(x) or decimal(x)", op)
		}
	}
	in.fail(line, "'%s' needs two numbers, but got %s and %s", op, typeName(l), typeName(r))
	return nil
}

func (in *Interp) intOp(line int, op string, a, b int64) Value {
	var c int64
	ok := true
	switch op {
	case "+":
		c, ok = addInt(a, b)
	case "-":
		c, ok = subInt(a, b)
	case "*":
		c, ok = mulInt(a, b)
	case "/":
		return in.decOp(line, op, decimalFromInt(a), decimalFromInt(b))
	case "%":
		if b == 0 {
			in.fail(line, "you can't divide by zero")
		}
		c = a % b
	}
	if !ok {
		in.fail(line, "this Int calculation is too big (Ints go up to 9,223,372,036,854,775,807). Use a Decimal for bigger numbers")
	}
	return box(c)
}

func (in *Interp) floatOp(line int, op string, a, b float64) Value {
	switch op {
	case "+":
		return a + b
	case "-":
		return a - b
	case "*":
		return a * b
	case "/":
		if b == 0 {
			in.fail(line, "you can't divide by zero")
		}
		return a / b
	case "%":
		if b == 0 {
			in.fail(line, "you can't divide by zero")
		}
		return math.Mod(a, b)
	}
	return nil
}

func (in *Interp) decOp(line int, op string, a, b *Decimal) Value {
	switch op {
	case "+":
		return a.Add(b)
	case "-":
		return a.Sub(b)
	case "*":
		return a.Mul(b)
	case "/":
		q, err := a.Div(b)
		if err != nil {
			in.fail(line, "%s", err.Error())
		}
		return q
	case "%":
		m, err := a.Mod(b)
		if err != nil {
			in.fail(line, "%s", err.Error())
		}
		return m
	}
	return nil
}

func (in *Interp) negate(line int, v Value) Value {
	switch n := v.(type) {
	case int64:
		if n == math.MinInt64 {
			in.fail(line, "this Int is too big to make negative")
		}
		return box(-n)
	case float64:
		return -n
	case *Decimal:
		return n.Neg()
	}
	in.fail(line, "'-' needs a number, but got %s", typeName(v))
	return nil
}

// wantInt accepts an Int, or a whole Decimal, as a count or position.
func (in *Interp) wantInt(line int, what string, v Value) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case *Decimal:
		if n.IsWhole() {
			if i, err := n.Int64(); err == nil {
				return i
			}
		}
	}
	in.fail(line, "%s must be a whole number (an Int), but got %s", what, describeValue(v))
	return 0
}

func describeValue(v Value) string {
	if isNumber(v) {
		return typeName(v) + " " + Text(v)
	}
	return typeName(v)
}
