package tool

import "testing"

func TestNormalizeNullableText(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "empty", value: "", want: ""},
		{name: "whitespace", value: " \t\n", want: ""},
		{name: "uppercase sentinel", value: "NULL", want: ""},
		{name: "lowercase sentinel", value: "null", want: ""},
		{name: "mixed sentinel with whitespace", value: "  NuLl\t", want: ""},
		{name: "normal value", value: "  nullable  ", want: "nullable"},
		{name: "contains sentinel word", value: "not null", want: "not null"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeNullableText(test.value); got != test.want {
				t.Fatalf("NormalizeNullableText(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestIsEmptyLikeText(t *testing.T) {
	for _, value := range []string{"", "  ", "NULL", " null "} {
		if !IsEmptyLikeText(value) {
			t.Fatalf("IsEmptyLikeText(%q) = false, want true", value)
		}
	}
	if IsEmptyLikeText("NULLABLE") {
		t.Fatal("IsEmptyLikeText() treated a normal value as empty")
	}
}
