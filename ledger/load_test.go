package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestLoadBudgetsRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{`, `null`, `{}`, `[] []`,
		`[{"category":"food","limit":0}]`,
		`[{"category":"food","limit":-1}]`,
		`[{"category":"food","limit_amount":10}]`,
		`[{"category":"food","limit":10},{"category":"bad","limit":0}]`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			// Неинициализированная БД: любая преждевременная запись вызовет панику.
			var service Service
			if err := service.LoadBudgets(context.Background(), strings.NewReader(body)); err == nil {
				t.Fatal("invalid import was accepted")
			}
		})
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read failure") }

func TestLoadBudgetsReaderAndEmptyArray(t *testing.T) {
	t.Parallel()
	var service Service
	if err := service.LoadBudgets(context.Background(), brokenReader{}); err == nil || !strings.Contains(err.Error(), "read failure") {
		t.Fatalf("reader error was lost: %v", err)
	}
	if err := service.LoadBudgets(context.Background(), strings.NewReader(`[]`)); err != nil {
		t.Fatal(err)
	}
}
