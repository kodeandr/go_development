package ledger

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrBudgetExceeded = errors.New("budget exceeded")

type Validatable interface {
	Validate() error
}

func CheckValid(v Validatable) error {
	if v == nil {
		return invalid("value is required")
	}
	return v.Validate()
}

var (
	_ Validatable = Budget{}
	_ Validatable = Transaction{}
)

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

type Budget struct {
	Category string `json:"category"`
	Limit    Money  `json:"limit"`
}

func (b Budget) Validate() error {
	if strings.TrimSpace(b.Category) == "" {
		return invalid("category is required")
	}
	if b.Limit <= 0 || b.Limit > MaxAmount {
		return invalid("limit must be between 0.01 and %s", MaxAmount)
	}
	return nil
}

type Transaction struct {
	ID          int64
	Amount      Money
	Category    string
	Description string
	Date        string
}

func (tx Transaction) Validate() error {
	if strings.TrimSpace(tx.Category) == "" {
		return invalid("category is required")
	}
	if tx.Amount <= 0 || tx.Amount > MaxAmount {
		return invalid("amount must be between 0.01 and %s", MaxAmount)
	}
	if date, err := time.Parse(time.DateOnly, tx.Date); err != nil || date.Year() < 1 {
		return invalid("date must have format YYYY-MM-DD and be a valid date")
	}
	return nil
}

type CategoryTotal struct {
	Category string `json:"category"`
	Amount   Money  `json:"amount"`
}

type ReportSummary struct {
	From       string          `json:"from"`
	To         string          `json:"to"`
	Total      Money           `json:"total"`
	ByCategory []CategoryTotal `json:"by_category"`
}

func ValidatePeriod(from, to string) error {
	start, err := time.Parse(time.DateOnly, from)
	if err != nil || start.Year() < 1 {
		return invalid("from must have format YYYY-MM-DD and be a valid date")
	}
	end, err := time.Parse(time.DateOnly, to)
	if err != nil || end.Year() < 1 {
		return invalid("to must have format YYYY-MM-DD and be a valid date")
	}
	if start.After(end) {
		return invalid("from must not be after to")
	}
	return nil
}
