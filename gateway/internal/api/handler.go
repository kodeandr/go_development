package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kodeandr/go_development/ledger"
)

type Ledger interface {
	SetBudget(context.Context, ledger.Budget) error
	ListBudgets(context.Context) ([]ledger.Budget, error)
	AddTransaction(context.Context, *ledger.Transaction) error
	ListTransactions(context.Context) ([]ledger.Transaction, error)
	GetReportSummary(context.Context, string, string) (ledger.ReportSummary, error)
}

type handler struct {
	ledger Ledger
	log    *slog.Logger
}

func New(service Ledger, health func(context.Context) error, logger *slog.Logger) http.Handler {
	h := &handler{ledger: service, log: logger}
	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		if err := health(r.Context()); err != nil {
			logger.Error("health check failed", "error", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "dependencies unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	routes := map[string]map[string]http.HandlerFunc{
		"/api/budgets":         {http.MethodPost: h.setBudget, http.MethodGet: h.listBudgets},
		"/api/transactions":    {http.MethodPost: h.addTransaction, http.MethodGet: h.listTransactions},
		"/api/reports/summary": {http.MethodGet: h.reportSummary},
		"/healthz":             {http.MethodGet: healthHandler},
		"/ping": {http.MethodGet: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "pong")
		}},
	}
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		methods, exists := routes[r.URL.Path]
		if !exists {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		next, exists := methods[r.Method]
		if !exists {
			allowed := make([]string, 0, len(methods))
			for method := range methods {
				allowed = append(allowed, method)
			}
			sort.Strings(allowed)
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		next(w, r.WithContext(ctx))
	})
	return Logging(logger)(router)
}

func (h *handler) setBudget(w http.ResponseWriter, r *http.Request) {
	var request CreateBudgetRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	budget := request.toDomain()
	budget.Category = strings.TrimSpace(budget.Category)
	if err := budget.Validate(); err != nil {
		h.writeError(w, err)
		return
	}
	if err := h.ledger.SetBudget(r.Context(), budget); err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, budgetResponse(budget))
}

func (h *handler) listBudgets(w http.ResponseWriter, r *http.Request) {
	budgets, err := h.ledger.ListBudgets(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}
	response := make([]BudgetResponse, 0, len(budgets))
	for _, budget := range budgets {
		response = append(response, budgetResponse(budget))
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) addTransaction(w http.ResponseWriter, r *http.Request) {
	var request CreateTransactionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	transaction := request.toDomain()
	if err := transaction.Validate(); err != nil {
		h.writeError(w, err)
		return
	}
	if err := h.ledger.AddTransaction(r.Context(), &transaction); err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, transactionResponse(transaction))
}

func (h *handler) listTransactions(w http.ResponseWriter, r *http.Request) {
	transactions, err := h.ledger.ListTransactions(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}
	response := make([]TransactionResponse, 0, len(transactions))
	for _, tx := range transactions {
		response = append(response, transactionResponse(tx))
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) reportSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := h.ledger.GetReportSummary(r.Context(), r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reportResponse(summary))
}

func decodeJSON(w http.ResponseWriter, r *http.Request, result any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request must contain one JSON object"})
		return false
	}
	return true
}

func (h *handler) writeError(w http.ResponseWriter, err error) {
	var validationError *ledger.ValidationError
	switch {
	case errors.Is(err, ledger.ErrBudgetExceeded):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "budget exceeded"})
	case errors.As(err, &validationError):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": validationError.Error()})
	default:
		h.log.Error("request failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
