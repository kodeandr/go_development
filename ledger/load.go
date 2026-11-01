package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

func (s *Service) LoadBudgets(ctx context.Context, r io.Reader) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	var budgets []Budget
	if err := decoder.Decode(&budgets); err != nil {
		return fmt.Errorf("read budgets JSON: %w", err)
	}
	if budgets == nil {
		return fmt.Errorf("budgets must be a JSON array")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("budgets file must contain one JSON array")
	}
	// Ошибки в данных обнаруживаются до первой записи в БД.
	for i, budget := range budgets {
		if err := CheckValid(budget); err != nil {
			return fmt.Errorf("budget %d: %w", i+1, err)
		}
	}
	for i, budget := range budgets {
		if err := s.SetBudget(ctx, budget); err != nil {
			return fmt.Errorf("save budget %d: %w", i+1, err)
		}
	}
	return nil
}
