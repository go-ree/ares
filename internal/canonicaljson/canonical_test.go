package canonicaljson

import (
	"strings"
	"testing"
)

func TestCanonicalizeIgnoresObjectOrderWhitespaceAndEquivalentNumbers(t *testing.T) {
	inputs := []string{
		`{"z":100,"a":{"b":0.00120,"a":-0},"items":[12.30,1e-100000]}`,
		` { "items" : [ 1.23e1, 10e-100001 ], "a" : { "a" : 0.0, "b" : 12e-4 }, "z" : 1e2 } `,
	}
	var want string
	for _, input := range inputs {
		got, err := Canonicalize([]byte(input))
		if err != nil {
			t.Fatalf("Canonicalize(%s) error = %v", input, err)
		}
		if want == "" {
			want = string(got)
		} else if string(got) != want {
			t.Fatalf("canonical JSON differs:\nfirst: %s\n got: %s", want, got)
		}
	}
	if strings.Repeat("0", 1000) == want || len(want) > 200 {
		t.Fatalf("large exponent was unexpectedly expanded: length=%d", len(want))
	}
}

func TestCanonicalizeWritesMathematicalIntegersAsPlainDecimal(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: `3600`, want: `3600`},
		{input: `3600.0`, want: `3600`},
		{input: `3.6e3`, want: `3600`},
		{input: `-3.600e3`, want: `-3600`},
		{input: `1200e-2`, want: `12`},
		{input: `-0e100`, want: `0`},
		{input: `9007199254740993`, want: `9007199254740993`},
		{input: `18446744073709551615`, want: `18446744073709551615`},
		{input: `18446744073709551616`, want: `18446744073709551616`},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := Canonicalize([]byte(test.input))
			if err != nil {
				t.Fatalf("Canonicalize(%s) error = %v", test.input, err)
			}
			if string(got) != test.want {
				t.Fatalf("Canonicalize(%s) = %s, want %s", test.input, got, test.want)
			}
		})
	}
}

func TestCanonicalizeKeepsEquivalentFractionalNumbersStable(t *testing.T) {
	inputs := []string{`1.2300`, `123e-2`, `0.123e1`}
	for _, input := range inputs {
		got, err := Canonicalize([]byte(input))
		if err != nil {
			t.Fatalf("Canonicalize(%s) error = %v", input, err)
		}
		if string(got) != `1.23` {
			t.Fatalf("Canonicalize(%s) = %s, want 1.23", input, got)
		}
	}

	got, err := Canonicalize([]byte(`-0.001200`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `-1.2e-3` {
		t.Fatalf("canonical small fraction = %s, want -1.2e-3", got)
	}
}

func TestCanonicalizeIntegerExpansionLimit(t *testing.T) {
	allowedInput := `1e4095`
	want := `1` + strings.Repeat(`0`, maxExpandedIntegerDigits-1)
	got, err := Canonicalize([]byte(allowedInput))
	if err != nil {
		t.Fatalf("Canonicalize(%s) error = %v", allowedInput, err)
	}
	if string(got) != want || len(got) != maxExpandedIntegerDigits {
		t.Fatalf("Canonicalize(%s) produced an invalid boundary integer of length %d", allowedInput, len(got))
	}

	for _, input := range []string{
		`1e4096`,
		`1e999999999999999999999999999999999999`,
	} {
		if got, err := Canonicalize([]byte(input)); err == nil {
			t.Fatalf("Canonicalize(%s) produced %d bytes, want a digit-limit error", input, len(got))
		} else if !strings.Contains(err.Error(), "4096 digit") {
			t.Fatalf("Canonicalize(%s) error = %v, want the explicit digit limit", input, err)
		}
	}
}

func TestCanonicalizeKeepsEquivalentLargeIntegerRepresentationsIdentical(t *testing.T) {
	inputs := []string{
		`18446744073709551616`,
		`18446744073709551616.0`,
		`1.8446744073709551616e19`,
		`184467440737095516160e-1`,
	}
	const want = `18446744073709551616`
	for _, input := range inputs {
		got, err := Canonicalize([]byte(input))
		if err != nil {
			t.Fatalf("Canonicalize(%s) error = %v", input, err)
		}
		if string(got) != want {
			t.Fatalf("Canonicalize(%s) = %s, want %s", input, got, want)
		}
	}
}

func TestCanonicalizePreservesLargeIntegerDifferences(t *testing.T) {
	first, err := Canonicalize([]byte(`{"value":9007199254740992}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Canonicalize([]byte(`{"value":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Fatalf("distinct large integers canonicalized identically: %s", first)
	}
}

func TestCanonicalizeRejectsInvalidOrMultipleValues(t *testing.T) {
	for _, input := range []string{`{"a":`, `{} {}`} {
		if _, err := Canonicalize([]byte(input)); err == nil {
			t.Fatalf("Canonicalize(%q) unexpectedly succeeded", input)
		}
	}
}
