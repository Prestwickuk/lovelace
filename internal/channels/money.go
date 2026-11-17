package channels

import (
	"fmt"
	"strconv"
	"strings"
)

// currencyMinorUnits lists non-2-decimal ISO 4217 currencies relevant to hospitality channels.
var currencyMinorUnits = map[string]int{
	"JPY": 0,
	"KRW": 0,
	"VND": 0,
	"ISK": 0,
	"CLP": 0,
	"BHD": 3,
	"JOD": 3,
	"KWD": 3,
	"OMR": 3,
	"TND": 3,
}

// CurrencyDecimals returns the ISO-4217 minor unit exponent for an uppercase 3-letter currency code.
func CurrencyDecimals(currency string) (int, error) {
	c := strings.ToUpper(strings.TrimSpace(currency))
	if len(c) != 3 {
		return 0, fmt.Errorf("%w: invalid currency code %q", ErrInvalidInput, currency)
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return 0, fmt.Errorf("%w: invalid currency code %q", ErrInvalidInput, currency)
		}
	}
	if d, ok := currencyMinorUnits[c]; ok {
		return d, nil
	}
	return 2, nil
}

// FormatMinorToDecimal converts integer minor units into an exact decimal string using integer arithmetic.
func FormatMinorToDecimal(minor int64, currency string) (string, error) {
	dec, err := CurrencyDecimals(currency)
	if err != nil {
		return "", err
	}
	if minor < 0 {
		return "", fmt.Errorf("%w: negative monetary amount %d", ErrInvalidInput, minor)
	}
	if dec == 0 {
		return strconv.FormatInt(minor, 10), nil
	}
	scale := pow10Int64(dec)
	whole := minor / scale
	frac := minor % scale
	return fmt.Sprintf("%d.%0*d", whole, dec, frac), nil
}

// FormatMinorToFloatJSON converts integer minor units cleanly via decimal string parsing for JSON APIs expecting numeric amounts.
func FormatMinorToNumber(minor int64, currency string) (float64, error) {
	s, err := FormatMinorToDecimal(minor, currency)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(s, 64)
}

// ParseDecimalToMinor parses a decimal or integer monetary string into exact integer minor units without floating-point drift.
func ParseDecimalToMinor(raw string, currency string) (int64, error) {
	dec, err := CurrencyDecimals(currency)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, nil
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = strings.TrimPrefix(s, "-")
	} else if strings.HasPrefix(s, "+") {
		s = strings.TrimPrefix(s, "+")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return 0, fmt.Errorf("%w: invalid decimal amount %q", ErrInvalidInput, raw)
	}
	wholeStr := parts[0]
	if wholeStr == "" {
		wholeStr = "0"
	}
	whole, err := strconv.ParseInt(wholeStr, 10, 64)
	if err != nil || whole < 0 {
		return 0, fmt.Errorf("%w: invalid decimal amount %q", ErrInvalidInput, raw)
	}
	fracStr := ""
	if len(parts) == 2 {
		fracStr = parts[1]
	}
	for _, r := range fracStr {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%w: invalid decimal amount %q", ErrInvalidInput, raw)
		}
	}
	if len(fracStr) > dec {
		// Permit trailing zeros beyond currency decimals (e.g., "100.500" -> 100.50), reject precision loss.
		extra := fracStr[dec:]
		for _, r := range extra {
			if r != '0' {
				return 0, fmt.Errorf("%w: fractional precision exceeds %d decimals in %q", ErrInvalidInput, dec, raw)
			}
		}
		fracStr = fracStr[:dec]
	}
	for len(fracStr) < dec {
		fracStr += "0"
	}
	var frac int64
	if dec > 0 && fracStr != "" {
		frac, err = strconv.ParseInt(fracStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: invalid decimal amount %q", ErrInvalidInput, raw)
		}
	}
	total := whole*pow10Int64(dec) + frac
	if neg {
		total = -total
	}
	return total, nil
}

func pow10Int64(n int) int64 {
	res := int64(1)
	for i := 0; i < n; i++ {
		res *= 10
	}
	return res
}
