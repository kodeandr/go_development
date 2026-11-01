package ledger

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
)

// Money хранит копейки, а в SQL и JSON передаётся десятичным числом.
type Money int64

const MaxAmount Money = 99_999_999_999_999 // NUMERIC(14, 2)

func ParseMoney(value string) (Money, error) {
	original := value
	negative := strings.HasPrefix(value, "-")
	if negative {
		value = value[1:]
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts[0]) == 0 {
		return 0, fmt.Errorf("invalid amount %q: use a decimal number with at most two fraction digits", original)
	}
	fraction := "00"
	if len(parts) == 2 {
		if len(parts[1]) == 0 || len(parts[1]) > 2 {
			return 0, fmt.Errorf("invalid amount %q: at most two fraction digits are allowed", original)
		}
		fraction = parts[1] + strings.Repeat("0", 2-len(parts[1]))
	}
	digits := parts[0] + fraction
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return 0, fmt.Errorf("invalid amount %q: expected a decimal number", original)
		}
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return 0, nil
	}
	if negative {
		digits = "-" + digits
	}
	cents, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount %q is out of range", original)
	}
	return Money(cents), nil
}

func (m Money) String() string {
	var cents uint64
	sign := ""
	if m < 0 {
		sign = "-"
		cents = uint64(-(m + 1)) + 1
	} else {
		cents = uint64(m)
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

func (m Money) MarshalJSON() ([]byte, error) { return []byte(m.String()), nil }

func (m *Money) UnmarshalJSON(data []byte) error {
	value, err := ParseMoney(string(data))
	if err == nil {
		*m = value
	}
	return err
}

func (m Money) Value() (driver.Value, error) { return m.String(), nil }

func (m *Money) Scan(src any) error {
	var value string
	switch src := src.(type) {
	case string:
		value = src
	case []byte:
		value = string(src)
	default:
		return fmt.Errorf("cannot scan %T as Money", src)
	}
	parsed, err := ParseMoney(value)
	if err == nil {
		*m = parsed
	}
	return err
}
