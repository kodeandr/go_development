package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/kodeandr/go_development/ledger/internal/cache"
)

type Service struct {
	db    *sql.DB
	cache *cache.Cache
	log   *slog.Logger
}

func newService(database *sql.DB, reportCache *cache.Cache, logger *slog.Logger) *Service {
	return &Service{db: database, cache: reportCache, log: logger}
}

func (s *Service) SetBudget(ctx context.Context, b Budget) error {
	b.Category = strings.TrimSpace(b.Category)
	if err := b.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin budget update: %w", err)
	}
	defer tx.Rollback()
	if err := lockCategory(ctx, tx, b.Category); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO budgets (category, limit_amount) VALUES ($1, $2)
		ON CONFLICT (category) DO UPDATE SET limit_amount = EXCLUDED.limit_amount`, b.Category, b.Limit)
	if err != nil {
		return fmt.Errorf("save budget: %w", err)
	}
	return tx.Commit()
}

func (s *Service) ListBudgets(ctx context.Context) ([]Budget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT category, limit_amount FROM budgets ORDER BY category`)
	if err != nil {
		return nil, fmt.Errorf("list budgets: %w", err)
	}
	defer rows.Close()
	budgets := make([]Budget, 0)
	for rows.Next() {
		var b Budget
		if err := rows.Scan(&b.Category, &b.Limit); err != nil {
			return nil, fmt.Errorf("read budget: %w", err)
		}
		budgets = append(budgets, b)
	}
	return budgets, rows.Err()
}

func (s *Service) AddTransaction(ctx context.Context, expense *Transaction) error {
	if expense == nil {
		return invalid("transaction is required")
	}
	expense.Category = strings.TrimSpace(expense.Category)
	if err := expense.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockCategory(ctx, tx, expense.Category); err != nil {
		return err
	}
	var exceeds bool
	// Если бюджета нет, SELECT не возвращает строку: категория не ограничена.
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE((SELECT SUM(amount) FROM expenses WHERE category = $1), 0) + $2::numeric > limit_amount
		FROM budgets WHERE category = $1`, expense.Category, expense.Amount).Scan(&exceeds)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check category budget: %w", err)
	}
	if exceeds {
		return ErrBudgetExceeded
	}
	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO expenses (amount, category, description, date)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		expense.Amount, expense.Category, expense.Description, expense.Date).Scan(&id)
	if err != nil {
		return fmt.Errorf("insert transaction: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	expense.ID = id
	cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := s.cache.InvalidateReports(cacheCtx); err != nil {
		s.log.Warn("could not invalidate report cache; old entries expire in 30s", "error", err)
	}
	return nil
}

func lockCategory(ctx context.Context, tx *sql.Tx, category string) error {
	// Одна блокировка для изменения бюджета и расходов, включая категории без бюджета.
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "cashcraft:category:"+category)
	if err != nil {
		return fmt.Errorf("lock category: %w", err)
	}
	return nil
}

func (s *Service) ListTransactions(ctx context.Context) ([]Transaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, amount, category, COALESCE(description, ''), date
		FROM expenses ORDER BY date DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()
	transactions := make([]Transaction, 0)
	for rows.Next() {
		var expense Transaction
		var date time.Time
		if err := rows.Scan(&expense.ID, &expense.Amount, &expense.Category, &expense.Description, &date); err != nil {
			return nil, fmt.Errorf("read transaction: %w", err)
		}
		expense.Date = date.Format(time.DateOnly)
		transactions = append(transactions, expense)
	}
	return transactions, rows.Err()
}

func (s *Service) GetReportSummary(ctx context.Context, from, to string) (ReportSummary, error) {
	if err := ValidatePeriod(from, to); err != nil {
		return ReportSummary{}, err
	}
	cacheCtx, cancel := context.WithTimeout(ctx, time.Second)
	key, keyErr := s.cache.ReportKey(cacheCtx, from, to)
	if keyErr == nil {
		var summary ReportSummary
		hit, err := s.cache.Get(cacheCtx, key, &summary)
		if err == nil && hit {
			cancel()
			s.log.Info("report summary", "cache", "hit", "from", from, "to", to)
			return summary, nil
		}
		if err != nil {
			s.log.Warn("report cache read failed; using PostgreSQL", "error", err)
		}
	} else {
		s.log.Warn("report cache unavailable; using PostgreSQL", "error", keyErr)
	}
	cancel()
	s.log.Info("report summary", "cache", "miss", "from", from, "to", to)
	rows, err := s.db.QueryContext(ctx, `
		SELECT category, SUM(amount) FROM expenses
		WHERE date >= $1 AND date <= $2 GROUP BY category ORDER BY category`, from, to)
	if err != nil {
		return ReportSummary{}, fmt.Errorf("calculate report: %w", err)
	}
	defer rows.Close()
	summary := ReportSummary{From: from, To: to, ByCategory: make([]CategoryTotal, 0)}
	for rows.Next() {
		var item CategoryTotal
		if err := rows.Scan(&item.Category, &item.Amount); err != nil {
			return ReportSummary{}, fmt.Errorf("read report row: %w", err)
		}
		if (item.Amount > 0 && summary.Total > Money(math.MaxInt64)-item.Amount) ||
			(item.Amount < 0 && summary.Total < Money(math.MinInt64)-item.Amount) {
			return ReportSummary{}, fmt.Errorf("report total is out of range")
		}
		summary.Total += item.Amount
		summary.ByCategory = append(summary.ByCategory, item)
	}
	if err := rows.Err(); err != nil {
		return ReportSummary{}, fmt.Errorf("read report: %w", err)
	}
	if keyErr == nil {
		writeCtx, writeCancel := context.WithTimeout(ctx, time.Second)
		defer writeCancel()
		if err := s.cache.Set(writeCtx, key, summary); err != nil {
			s.log.Warn("report cache write failed", "error", err)
		}
	}
	return summary, nil
}
