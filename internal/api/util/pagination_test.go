package util

import (
	"encoding/json"
	"math"
	"testing"
)

func pageInteger(value int64) PageInteger {
	var result PageInteger
	result.set(value)
	return result
}

func TestParamPageJSONRejectsNonIntegerRepresentations(t *testing.T) {
	for _, payload := range []string{
		`{"page_num":null,"page_size":10}`,
		`{"page_num":1,"page_size":null}`,
		`{"page_num":"1","page_size":10}`,
		`{"page_num":1.5,"page_size":10}`,
		`{"page_num":1e2,"page_size":10}`,
		`{"page_num":true,"page_size":10}`,
		`{"page_num":1,"page_size":"10"}`,
		`{"page_num":1,"page_size":10.5}`,
	} {
		var params ParamPage
		if err := json.Unmarshal([]byte(payload), &params); err == nil {
			t.Errorf("json.Unmarshal(%s) accepted a non-integer pagination value", payload)
		}
	}
}

func TestNormalizePaginationAppliesDefaultsOnlyWhenOmitted(t *testing.T) {
	params := ParamPage{}
	pageSize, offset, err := (&ParamPage{}).NormalizePagination(&params)
	if err != nil {
		t.Fatal(err)
	}
	if params.GetPageNum() != int(DefaultPageNum) || pageSize != int(DefaultPageSize) || offset != 0 {
		t.Fatalf("defaults = page:%d size:%d offset:%d", params.GetPageNum(), pageSize, offset)
	}
}

func TestNormalizePaginationRejectsInvalidRangesAndOverflow(t *testing.T) {
	tests := []struct {
		name     string
		pageNum  int64
		pageSize int64
	}{
		{name: "zero page", pageNum: 0, pageSize: 10},
		{name: "negative page", pageNum: -1, pageSize: 10},
		{name: "zero size", pageNum: 1, pageSize: 0},
		{name: "negative size", pageNum: 1, pageSize: -1},
		{name: "size above maximum", pageNum: 1, pageSize: MaxPageSize + 1},
		{name: "offset overflow", pageNum: math.MaxInt64, pageSize: MaxPageSize},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := ParamPage{PageNum: pageInteger(test.pageNum), PageSize: pageInteger(test.pageSize)}
			if _, _, err := (&ParamPage{}).NormalizePagination(&params); err == nil {
				t.Fatal("NormalizePagination() accepted invalid pagination")
			}
		})
	}
}

func TestNormalizePaginationComputesBoundedOffset(t *testing.T) {
	params := ParamPage{PageNum: pageInteger(3), PageSize: pageInteger(200)}
	pageSize, offset, err := (&ParamPage{}).NormalizePagination(&params)
	if err != nil {
		t.Fatal(err)
	}
	if pageSize != 200 || offset != 400 || params.GetPageNum() != 3 {
		t.Fatalf("pagination = page:%d size:%d offset:%d", params.GetPageNum(), pageSize, offset)
	}
}

func TestCalculateTotalPagesDoesNotOverflow(t *testing.T) {
	if got := (&ParamPage{}).CalculateTotalPages(math.MaxInt64, 200); got <= 0 {
		t.Fatalf("CalculateTotalPages() = %d, want a positive bounded result", got)
	}
}
