package ledger

import (
	"encoding/json"
	"testing"
)

func TestMoney(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		input string
		cents Money
		want  string
	}{
		{"0", 0, "0.00"},
		{"0.1", 10, "0.10"},
		{"0.20", 20, "0.20"},
		{"-0.01", -1, "-0.01"},
		{"12345.67", 1_234_567, "12345.67"},
		{"999999999999.99", MaxAmount, "999999999999.99"},
		{"-92233720368547758.08", Money(-1 << 63), "-92233720368547758.08"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			var amount Money
			if err := json.Unmarshal([]byte(tc.input), &amount); err != nil {
				t.Fatal(err)
			}
			if amount != tc.cents {
				t.Fatalf("got %d cents, want %d", amount, tc.cents)
			}
			encoded, err := json.Marshal(amount)
			if err != nil || string(encoded) != tc.want {
				t.Fatalf("encoded %s, error %v; want %s", encoded, err, tc.want)
			}
			var scanned Money
			if err := scanned.Scan([]byte(tc.want)); err != nil || scanned != amount {
				t.Fatalf("SQL scan: %s, %v", scanned, err)
			}
			value, err := amount.Value()
			if err != nil || value != tc.want {
				t.Fatalf("SQL value: %v, %v", value, err)
			}
		})
	}
	if Money(10)+Money(20) != Money(30) {
		t.Fatal("0.10 + 0.20 must equal 0.30")
	}
}

func TestMoneyRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", "null", `"12.50"`, "1.001", "1e2", "+1", "-", "1.", ".50", "NaN", "92233720368547758.08"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseMoney(input); err == nil {
				t.Fatalf("accepted invalid amount %q", input)
			}
		})
	}
	var amount Money
	if err := amount.Scan(float64(0.1)); err == nil {
		t.Fatal("float scan would lose precision")
	}
}

func TestTransactionValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		tx   Transaction
		ok   bool
	}{
		{"valid", Transaction{Category: "food", Amount: 100, Date: "2026-01-01"}, true},
		{"zero", Transaction{Category: "food", Date: "2026-01-01"}, false},
		{"negative", Transaction{Category: "food", Amount: -100, Date: "2026-01-01"}, false},
		{"empty category", Transaction{Amount: 100, Date: "2026-01-01"}, false},
		{"blank category", Transaction{Category: " ", Amount: 100, Date: "2026-01-01"}, false},
		{"bad date", Transaction{Category: "food", Amount: 100, Date: "2026-02-30"}, false},
		{"year zero", Transaction{Category: "food", Amount: 100, Date: "0000-01-01"}, false},
		{"leap date", Transaction{Category: "food", Amount: 100, Date: "2024-02-29"}, true},
		{"too large", Transaction{Category: "food", Amount: MaxAmount + 1, Date: "2026-01-01"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.tx.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v; valid = %v", err, tc.ok)
			}
		})
	}
}

func TestBudgetValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		b    Budget
		ok   bool
	}{
		{"valid", Budget{Category: "food", Limit: 100}, true},
		{"zero", Budget{Category: "food"}, false},
		{"negative", Budget{Category: "food", Limit: -1}, false},
		{"blank category", Budget{Category: " ", Limit: 100}, false},
		{"too large", Budget{Category: "food", Limit: MaxAmount + 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.b.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v; valid = %v", err, tc.ok)
			}
		})
	}
}

func TestCheckValid(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		v    Validatable
		ok   bool
	}{
		{"budget", Budget{Category: "food", Limit: 100}, true},
		{"transaction", Transaction{Category: "food", Amount: 100, Date: "2026-01-01"}, true},
		{"bad budget", Budget{}, false},
		{"bad transaction", Transaction{}, false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := CheckValid(tc.v); (err == nil) != tc.ok {
				t.Fatalf("CheckValid() = %v; valid = %v", err, tc.ok)
			}
		})
	}
}

func TestValidatePeriod(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		from, to string
		ok       bool
	}{
		{"2026-01-01", "2026-01-01", true},
		{"2026-01-01", "2026-01-31", true},
		{"", "2026-01-01", false},
		{"2026-01-02", "2026-01-01", false},
		{"2026-01-01", "2026-02-30", false},
		{"0000-01-01", "2026-01-01", false},
	} {
		t.Run(tc.from+"/"+tc.to, func(t *testing.T) {
			t.Parallel()
			if err := ValidatePeriod(tc.from, tc.to); (err == nil) != tc.ok {
				t.Fatalf("ValidatePeriod() = %v; valid = %v", err, tc.ok)
			}
		})
	}
}
