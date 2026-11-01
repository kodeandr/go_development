package api

import "github.com/kodeandr/go_development/ledger"

type CreateTransactionRequest struct {
	Amount      ledger.Money `json:"amount"`
	Category    string       `json:"category"`
	Description string       `json:"description"`
	Date        string       `json:"date"`
}

type TransactionResponse struct {
	ID          int64        `json:"id"`
	Amount      ledger.Money `json:"amount"`
	Category    string       `json:"category"`
	Description string       `json:"description"`
	Date        string       `json:"date"`
}

type CreateBudgetRequest struct {
	Category string       `json:"category"`
	Limit    ledger.Money `json:"limit"`
}

type BudgetResponse struct {
	Category string       `json:"category"`
	Limit    ledger.Money `json:"limit"`
}

type CategoryTotalResponse struct {
	Category string       `json:"category"`
	Amount   ledger.Money `json:"amount"`
}

type ReportSummaryResponse struct {
	From       string                  `json:"from"`
	To         string                  `json:"to"`
	Total      ledger.Money            `json:"total"`
	ByCategory []CategoryTotalResponse `json:"by_category"`
}

func (request CreateTransactionRequest) toDomain() ledger.Transaction {
	return ledger.Transaction{Amount: request.Amount, Category: request.Category, Description: request.Description, Date: request.Date}
}

func transactionResponse(tx ledger.Transaction) TransactionResponse {
	return TransactionResponse{ID: tx.ID, Amount: tx.Amount, Category: tx.Category, Description: tx.Description, Date: tx.Date}
}

func (request CreateBudgetRequest) toDomain() ledger.Budget {
	return ledger.Budget{Category: request.Category, Limit: request.Limit}
}

func budgetResponse(budget ledger.Budget) BudgetResponse {
	return BudgetResponse{Category: budget.Category, Limit: budget.Limit}
}

func reportResponse(report ledger.ReportSummary) ReportSummaryResponse {
	response := ReportSummaryResponse{From: report.From, To: report.To, Total: report.Total, ByCategory: make([]CategoryTotalResponse, 0, len(report.ByCategory))}
	for _, category := range report.ByCategory {
		response.ByCategory = append(response.ByCategory, CategoryTotalResponse{Category: category.Category, Amount: category.Amount})
	}
	return response
}
