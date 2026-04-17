package audit

import (
	"context"
	"time"
)

// apiAuditor is the built-in APIAuditor implementation. It redacts headers and
// body fields, truncates oversized payloads, and persists the resulting row
// via the configured Store.
type apiAuditor struct {
	a *auditor
}

func (p *apiAuditor) Record(ctx context.Context, entry APIEntry) error {
	if !p.a.cfg.APIAudit.Enabled {
		return nil
	}
	cfg := p.a.cfg.APIAudit

	headers := redactHeaders(entry.RequestHeaders, cfg.RedactHeaders)
	reqBody := redactBody(entry.RequestBody, cfg.RedactBodyFields)
	respBody := redactBody(entry.ResponseBody, cfg.RedactBodyFields)

	headersJSON, err := jsonMarshal(headers)
	if err != nil {
		return err
	}
	reqJSON, err := jsonMarshal(reqBody)
	if err != nil {
		return err
	}
	respJSON, err := jsonMarshal(respBody)
	if err != nil {
		return err
	}

	reqJSON, err = truncateJSON(reqJSON, cfg.MaxBodySize)
	if err != nil {
		return err
	}
	respJSON, err = truncateJSON(respJSON, cfg.MaxBodySize)
	if err != nil {
		return err
	}

	metaJSON, err := jsonMarshal(entry.Metadata)
	if err != nil {
		return err
	}

	userID, _ := p.a.resolveUser(ctx)
	txID := entry.TransactionID
	if txID == "" {
		txID = TransactionIDFromContext(ctx)
	}

	row := AuditAPILog{
		Service:        entry.Service,
		Endpoint:       entry.Endpoint,
		Method:         entry.Method,
		StatusCode:     entry.StatusCode,
		RequestHeaders: headersJSON,
		RequestBody:    reqJSON,
		ResponseBody:   respJSON,
		DurationMs:     entry.DurationMs,
		ErrorMessage:   entry.ErrorMessage,
		UserID:         userID,
		Metadata:       metaJSON,
		TransactionID:  txID,
		CreatedAt:      time.Now().UTC(),
	}
	return p.a.handleError(cfg.OnError,
		p.a.store.SaveAPILog(ctx, cfg.Table, row),
		"save api log")
}

func (p *apiAuditor) Query(ctx context.Context, filter APIFilter) ([]AuditAPILog, error) {
	return p.a.store.QueryAPILogs(ctx, p.a.cfg.APIAudit.Table, filter)
}
