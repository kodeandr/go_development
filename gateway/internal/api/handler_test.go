package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kodeandr/go_development/ledger"
)

type stubLedger struct {
	err   error
	calls int
}

func (s *stubLedger) SetBudget(_ context.Context, b ledger.Budget) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	return b.Validate()
}

func (s *stubLedger) ListBudgets(context.Context) ([]ledger.Budget, error) {
	return []ledger.Budget{}, s.err
}

func (s *stubLedger) AddTransaction(_ context.Context, tx *ledger.Transaction) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	if err := tx.Validate(); err != nil {
		return err
	}
	tx.ID = 7
	return nil
}

func (s *stubLedger) ListTransactions(context.Context) ([]ledger.Transaction, error) {
	return []ledger.Transaction{}, s.err
}

func (s *stubLedger) GetReportSummary(_ context.Context, from, to string) (ledger.ReportSummary, error) {
	return ledger.ReportSummary{}, ledger.ValidatePeriod(from, to)
}

func TestHTTPContract(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		serviceErr               error
		status                   int
		contains                 string
	}{
		{"budget", "POST", "/api/budgets", `{"category":"food","limit":100}`, nil, 201, `"limit":100.00`},
		{"transaction", "POST", "/api/transactions", `{"amount":10.25,"category":"food","description":"lunch","date":"2026-01-01"}`, nil, 201, `"id":7`},
		{"limit", "POST", "/api/transactions", `{"amount":10,"category":"food","date":"2026-01-01"}`, ledger.ErrBudgetExceeded, 409, `"error":"budget exceeded"`},
		{"validation", "POST", "/api/budgets", `{"category":"food","limit":0}`, nil, 400, "limit"},
		{"negative amount", "POST", "/api/transactions", `{"amount":-10,"category":"food","date":"2026-01-01"}`, nil, 400, "amount"},
		{"storage error", "GET", "/api/budgets", "", errors.New("private database details"), 500, `"error":"internal server error"`},
		{"empty budgets", "GET", "/api/budgets", "", nil, 200, "[]"},
		{"empty transactions", "GET", "/api/transactions", "", nil, 200, "[]"},
		{"bad period", "GET", "/api/reports/summary?from=2026-02-01&to=2026-01-01", "", nil, 400, "from must not be after to"},
		{"method", "DELETE", "/api/budgets", "", nil, 405, "method not allowed"},
		{"unknown route", "GET", "/api/missing", "", nil, 404, "not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &stubLedger{err: tc.serviceErr}
			h := New(service, func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.contains) {
				t.Fatalf("got %d %s; want %d containing %q", response.Code, response.Body, tc.status, tc.contains)
			}
			if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q", got)
			}
			if strings.Contains(response.Body.String(), "private database details") {
				t.Fatal("internal error was exposed to the client")
			}
		})
	}
}

func TestPingAndLogging(t *testing.T) {
	var logs bytes.Buffer
	h := New(&stubLedger{}, func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(&logs, nil)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if w.Code != 200 || w.Body.String() != "pong" {
		t.Fatalf("ping: %d %s", w.Code, w.Body)
	}
	for _, field := range []string{"method=GET", "path=/ping", "duration="} {
		if !strings.Contains(logs.String(), field) {
			t.Fatalf("missing %s in request log: %s", field, logs.String())
		}
	}
}

func TestRejectMalformedJSONBeforeStorage(t *testing.T) {
	for _, body := range []string{
		`{`,
		`{"category":"food","limit":1.001}`,
		`{"category":"food","limit":100,"unknown":true}`,
		`{"category":"food","limit":100} {}`,
	} {
		t.Run(body, func(t *testing.T) {
			s := &stubLedger{}
			h := New(s, func(context.Context) error { return nil }, slog.Default())
			r := httptest.NewRequest(http.MethodPost, "/api/budgets", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 400 || s.calls != 0 {
				t.Fatalf("status %d, storage calls %d", w.Code, s.calls)
			}
		})
	}
}

func TestHealthAndContentType(t *testing.T) {
	h := New(&stubLedger{}, func(context.Context) error { return errors.New("connection lost") }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/healthz", 503}, {"POST", "/api/budgets", 415}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))
		if w.Code != tc.status {
			t.Fatalf("got %d, want %d", w.Code, tc.status)
		}
	}
}
