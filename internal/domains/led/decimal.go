package led

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
)

func parsePositiveFixed(value string, scale int, allowZero bool) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, errors.New("invalid decimal")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("invalid decimal")
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return 0, errors.New("invalid decimal")
			}
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || len(fraction) > scale {
			return 0, errors.New("invalid decimal scale")
		}
	}
	fraction += strings.Repeat("0", scale-len(fraction))
	digits := strings.TrimLeft(parts[0]+fraction, "0")
	if digits == "" {
		if allowZero {
			return 0, nil
		}
		return 0, errors.New("decimal must be greater than zero")
	}
	parsed, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || parsed == math.MaxInt64 {
		return 0, errors.New("decimal out of range")
	}
	return parsed, nil
}

func lineAmountCents(quantity, unitPrice int64) (int64, error) {
	product := new(big.Int).Mul(big.NewInt(quantity), big.NewInt(unitPrice))
	divisor := big.NewInt(1_000_000)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(product, divisor, remainder)
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(divisor) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() || quotient.Sign() <= 0 {
		return 0, errors.New("line amount out of range")
	}
	return quotient.Int64(), nil
}

func formatQuantity(value int64) string { return formatSignedFixed(value, 6) }

func formatMoney(value int64) string { return formatSignedFixed(value, 2) }

func formatAbsoluteQuantity(value int64) string {
	if value < 0 {
		value = -value
	}
	return formatQuantity(value)
}

func formatAbsoluteMoney(value int64) string {
	if value < 0 {
		value = -value
	}
	return formatMoney(value)
}

func formatSignedFixed(value int64, scale int) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	divisor := int64(1)
	for range scale {
		divisor *= 10
	}
	whole, fraction := value/divisor, value%divisor
	text := strconv.FormatInt(whole, 10) + "." + leftPad(strconv.FormatInt(fraction, 10), scale)
	if scale > 2 {
		text = strings.TrimRight(text, "0")
		if strings.HasSuffix(text, ".") {
			text += "0"
		}
	}
	return sign + text
}

func leftPad(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return strings.Repeat("0", width-len(value)) + value
}
