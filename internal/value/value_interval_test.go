package value_test

import (
	"testing"

	"github.com/goccy/googlesqlite/internal/intervalvalue"
	"github.com/goccy/googlesqlite/internal/value"
)

// TestIntervalValue covers IntervalValue's all-unsupported arithmetic,
// the conversion stubs that return errors, and ToString / ToJSON
// formatting including the negative-month sign-fixup branch.
func TestIntervalValue(t *testing.T) {
	t.Parallel()

	iv := &value.IntervalValue{IntervalValue: &intervalvalue.IntervalValue{Months: 2, Days: 3}}

	t.Run("arithmetic and comparison", func(t *testing.T) {
		// INTERVAL compares with 30-day months and 24-hour days
		// (data-types.md, interval type); + and - work part by part.
		month := &value.IntervalValue{IntervalValue: &intervalvalue.IntervalValue{Months: 1}}
		days30 := &value.IntervalValue{IntervalValue: &intervalvalue.IntervalValue{Days: 30}}
		if eq, err := month.EQ(days30); err != nil || !eq {
			t.Fatalf("1 MONTH = 30 DAY: %v %v", eq, err)
		}
		if gt, err := iv.GT(month); err != nil || !gt {
			t.Fatalf("2 MONTH 3 DAY > 1 MONTH: %v %v", gt, err)
		}
		if lte, err := month.LTE(iv); err != nil || !lte {
			t.Fatalf("1 MONTH <= 2 MONTH 3 DAY: %v %v", lte, err)
		}
		sum, err := iv.Add(month)
		if err != nil {
			t.Fatal(err)
		}
		if s, _ := sum.ToString(); s != "0-3 3 0:0:0" {
			t.Fatalf("Add = %q", s)
		}
		diff, err := month.Sub(iv)
		if err != nil {
			t.Fatal(err)
		}
		if s, _ := diff.ToString(); s != "-0-1 -3 0:0:0" {
			t.Fatalf("Sub = %q", s)
		}
		if _, err := iv.Mul(iv); err == nil {
			t.Fatal("Mul")
		}
		if _, err := iv.Div(iv); err == nil {
			t.Fatal("Div")
		}
		if _, err := iv.EQ(value.IntValue(1)); err == nil {
			t.Fatal("EQ with INT64 should fail")
		}
	})

	t.Run("ToInt64/Float64/Bool/Array/Struct/Time/Rat error", func(t *testing.T) {
		if _, err := iv.ToInt64(); err == nil {
			t.Fatal("ToInt64")
		}
		if _, err := iv.ToFloat64(); err == nil {
			t.Fatal("ToFloat64")
		}
		if _, err := iv.ToBool(); err == nil {
			t.Fatal("ToBool")
		}
		if _, err := iv.ToArray(); err == nil {
			t.Fatal("ToArray")
		}
		if _, err := iv.ToStruct(); err == nil {
			t.Fatal("ToStruct")
		}
		if _, err := iv.ToTime(); err == nil {
			t.Fatal("ToTime")
		}
		if _, err := iv.ToRat(); err == nil {
			t.Fatal("ToRat")
		}
	})

	t.Run("ToString/ToBytes/ToJSON", func(t *testing.T) {
		s, _ := iv.ToString()
		if s == "" {
			t.Fatal("ToString empty")
		}
		b, _ := iv.ToBytes()
		if string(b) != s {
			t.Fatalf("ToBytes mismatch: %s vs %s", b, s)
		}
		j, _ := iv.ToJSON()
		// JSON is the string in quotes
		if len(j) < 2 || j[0] != '"' || j[len(j)-1] != '"' {
			t.Fatalf("ToJSON not quoted: %s", j)
		}
	})

	t.Run("ToString negative months fix-up", func(t *testing.T) {
		// Years==0 && Months<0 triggers the leading-minus branch.
		neg := &value.IntervalValue{IntervalValue: &intervalvalue.IntervalValue{Months: -1}}
		s, _ := neg.ToString()
		if s == "" || s[0] != '-' {
			t.Fatalf("ToString negative: %q", s)
		}
	})

	t.Run("Format/Interface", func(t *testing.T) {
		if iv.Format('t') == "" {
			t.Fatal("Format empty")
		}
		if iv.Interface() == nil {
			t.Fatal("Interface nil")
		}
	})
}
