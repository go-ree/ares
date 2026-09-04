// Package canonicaljson produces a deterministic representation for JSON
// integrity checks. It is semantic: insignificant whitespace, object key order
// and equivalent number spellings do not affect the result.
package canonicaljson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
)

// maxExpandedIntegerDigits bounds the only normalization path whose output can
// be much larger than its input (for example, 1e100000). Values beyond this
// limit are rejected instead of being rounded or silently kept in a form that
// MySQL may rewrite as a JSON number incompatible with Go integer fields.
const maxExpandedIntegerDigits = 4096

func Canonicalize(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("JSON contains multiple values")
		}
		return nil, fmt.Errorf("decode trailing JSON: %w", err)
	}
	normalized, err := normalize(value)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return encoded, nil
}

func normalize(value any) (any, error) {
	switch typed := value.(type) {
	case json.Number:
		return normalizeNumber(typed)
	case map[string]any:
		for key, nested := range typed {
			normalized, err := normalize(nested)
			if err != nil {
				return nil, fmt.Errorf("normalize object field %q: %w", key, err)
			}
			typed[key] = normalized
		}
		return typed, nil
	case []any:
		for index, nested := range typed {
			normalized, err := normalize(nested)
			if err != nil {
				return nil, fmt.Errorf("normalize array index %d: %w", index, err)
			}
			typed[index] = normalized
		}
		return typed, nil
	default:
		return value, nil
	}
}

func normalizeNumber(number json.Number) (json.Number, error) {
	raw := string(number)
	sign := ""
	if strings.HasPrefix(raw, "-") {
		sign = "-"
		raw = raw[1:]
	}
	mantissa, exponentText := raw, "0"
	if index := strings.IndexAny(raw, "eE"); index >= 0 {
		mantissa, exponentText = raw[:index], raw[index+1:]
	}
	exponent := new(big.Int)
	if _, ok := exponent.SetString(exponentText, 10); !ok {
		return "", fmt.Errorf("invalid JSON number %q", number)
	}
	integer, fraction := mantissa, ""
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		integer, fraction = mantissa[:index], mantissa[index+1:]
	}
	digits := strings.TrimLeft(integer+fraction, "0")
	if digits == "" {
		return json.Number("0"), nil
	}
	scale := new(big.Int).Sub(exponent, big.NewInt(int64(len(fraction))))
	trailingZeros := len(digits) - len(strings.TrimRight(digits, "0"))
	if trailingZeros > 0 {
		digits = digits[:len(digits)-trailingZeros]
		scale.Add(scale, big.NewInt(int64(trailingZeros)))
	}
	if scale.Sign() >= 0 {
		if len(digits) > maxExpandedIntegerDigits ||
			scale.Cmp(big.NewInt(int64(maxExpandedIntegerDigits-len(digits)))) > 0 {
			return "", fmt.Errorf("JSON integer exceeds the %d digit canonicalization limit", maxExpandedIntegerDigits)
		}
		return json.Number(sign + digits + strings.Repeat("0", int(scale.Int64()))), nil
	}
	scientificExponent := new(big.Int).Add(scale, big.NewInt(int64(len(digits)-1)))
	coefficient := digits[:1]
	if len(digits) > 1 {
		coefficient += "." + digits[1:]
	}
	if scientificExponent.Sign() != 0 {
		coefficient += "e" + scientificExponent.String()
	}
	return json.Number(sign + coefficient), nil
}
