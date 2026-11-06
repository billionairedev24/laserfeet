package lang

import (
	"math"
	"testing"
)

func dec(t *testing.T, s string) *Decimal {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDecimalIsExact(t *testing.T) {
	if got := dec(t, "0.1").Add(dec(t, "0.2")).String(); got != "0.3" {
		t.Errorf("0.1 + 0.2 = %s", got)
	}
	if got := dec(t, "19.99").Mul(decimalFromInt(3)).String(); got != "59.97" {
		t.Errorf("19.99 * 3 = %s", got)
	}
	if got := dec(t, "12.50").Add(decimalFromInt(1)).String(); got != "13.50" {
		t.Errorf("money keeps its places: %s", got)
	}
	q, _ := decimalFromInt(10).Div(decimalFromInt(4))
	if q.String() != "2.5" {
		t.Errorf("10 / 4 = %s", q)
	}
	third, _ := decimalFromInt(1).Div(decimalFromInt(3))
	if third.String() != "0.3333333333333333333333333333" {
		t.Errorf("1 / 3 = %s", third)
	}
	if _, err := decimalFromInt(1).Div(decimalFromInt(0)); err == nil {
		t.Error("dividing by zero must fail")
	}
	for in, want := range map[string]string{"2.5": "3", "-2.5": "-3", "2.449": "2", "1.005": "1"} {
		if got := dec(t, in).Round(0).String(); got != want {
			t.Errorf("round(%s) = %s, want %s", in, got, want)
		}
	}
	if got := dec(t, "1.005").Round(2).String(); got != "1.01" {
		t.Errorf("round(1.005, 2) = %s (exact decimals round correctly, unlike floats)", got)
	}
	if dec(t, "2.50").Cmp(dec(t, "2.5")) != 0 {
		t.Error("2.50 and 2.5 are the same number")
	}
	if got := dec(t, "-0.05").String(); got != "-0.05" {
		t.Errorf("got %s", got)
	}
}

func TestIntOverflowIsCaught(t *testing.T) {
	if _, ok := addInt(math.MaxInt64, 1); ok {
		t.Error("MaxInt64 + 1 must overflow")
	}
	if _, ok := mulInt(math.MaxInt64/2+1, 2); ok {
		t.Error("multiplication overflow must be caught")
	}
	if _, ok := subInt(math.MinInt64, 1); ok {
		t.Error("MinInt64 - 1 must overflow")
	}
	if c, ok := mulInt(-4, 5); !ok || c != -20 {
		t.Error("-4 * 5")
	}
}
