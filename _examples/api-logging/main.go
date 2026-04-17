// Example: third-party API call auditing with header / body redaction and
// transaction correlation against data changes.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gopackx/go-audit"
	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", ":memory:")
	must(err)

	auditor, err := audit.New(db, audit.Config{
		Dialect:  audit.SQLite,
		UserFunc: func(ctx context.Context) (string, string) { return "alice", "user" },
		DataAudit: audit.DataAuditConfig{
			Enabled: true,
		},
		APIAudit: audit.APIAuditConfig{
			Enabled:          true,
			RedactHeaders:    []string{"Authorization", "X-API-Key"},
			RedactBodyFields: []string{"token", "card_number"},
			MaxBodySize:      4096,
		},
	})
	must(err)
	must(auditor.AutoMigrate(context.Background()))

	// Fake third-party API for the demo.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ref_no":"TRF-001","status":"success"}`))
	}))
	defer server.Close()

	txID := audit.NewTransactionID()
	ctx := audit.WithTransactionID(context.Background(), txID)

	// 1. Data change: create the pending transaction row.
	must(auditor.RecordDataChange(ctx, audit.DataEntry{
		EntityType: "transactions",
		EntityID:   "tx-42",
		Action:     audit.ActionCreate,
		NewValues: map[string]any{
			"status": "pending",
			"amount": 500000,
		},
	}))

	// 2. Third-party API call — audited with redacted headers + body.
	start := time.Now()
	resp, _, err := call(ctx, server.URL+"/transfer", map[string]any{
		"account_to":  "1234567890",
		"amount":      500000,
		"token":       "secret-token-should-be-redacted",
		"card_number": "4111-1111-1111-1111",
	})
	must(err)
	must(auditor.API().Record(ctx, audit.APIEntry{
		Service:        "bca",
		Endpoint:       "/transfer",
		Method:         "POST",
		StatusCode:     resp.StatusCode,
		RequestHeaders: map[string]string{"Authorization": "Bearer super-secret", "Content-Type": "application/json"},
		RequestBody: map[string]any{
			"account_to":  "1234567890",
			"amount":      500000,
			"token":       "secret-token-should-be-redacted",
			"card_number": "4111-1111-1111-1111",
		},
		ResponseBody: map[string]any{"ref_no": "TRF-001", "status": "success"},
		DurationMs:   int(time.Since(start).Milliseconds()),
	}))

	// 3. Data change: mark transaction complete.
	must(auditor.RecordDataChange(ctx, audit.DataEntry{
		EntityType: "transactions",
		EntityID:   "tx-42",
		Action:     audit.ActionUpdate,
		OldValues:  map[string]any{"status": "pending"},
		NewValues:  map[string]any{"status": "success"},
	}))

	// Pull everything tied to this transaction.
	bundle, err := auditor.QueryByTransaction(ctx, txID)
	must(err)

	fmt.Printf("transaction %s:\n", bundle.TransactionID)
	fmt.Printf("  data changes (%d):\n", len(bundle.DataLogs))
	for _, l := range bundle.DataLogs {
		fmt.Printf("    [%s] %s#%s  old=%s new=%s\n",
			l.Action, l.EntityType, l.EntityID,
			jsonOrEmpty(l.OldValues), jsonOrEmpty(l.NewValues))
	}
	fmt.Printf("  api calls (%d):\n", len(bundle.APILogs))
	for _, l := range bundle.APILogs {
		fmt.Printf("    %s %s %s -> %d (%dms)  headers=%s body=%s\n",
			l.Service, l.Method, l.Endpoint, l.StatusCode, l.DurationMs,
			jsonOrEmpty(l.RequestHeaders), jsonOrEmpty(l.RequestBody))
	}
}

func call(ctx context.Context, url string, body any) (*http.Response, []byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	return resp, nil, nil
}

func jsonOrEmpty(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
