package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gopackx/go-audit/dialect"
	"github.com/gopackx/go-audit/entry"
)

// identRE limits table names to SQL identifiers the package is willing to
// interpolate directly into DML. Rejects anything that could open a second
// statement or close-quote the identifier.
var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// NewSQLStore returns a Store backed by database/sql using the supplied
// dialect for parameter placeholders.
func NewSQLStore(db *sql.DB, d dialect.Dialect) Store {
	return &sqlStore{db: db, d: d}
}

type sqlStore struct {
	db *sql.DB
	d  dialect.Dialect
}

func (s *sqlStore) Exec(ctx context.Context, stmt string) error {
	_, err := s.db.ExecContext(ctx, stmt)
	return err
}

func (s *sqlStore) SaveDataLog(ctx context.Context, table string, log entry.AuditLog) error {
	cols := []string{
		"entity_type", "entity_id", "action", "old_values", "new_values",
		"user_id", "user_type", "metadata", "transaction_id", "created_at",
	}
	ph := make([]string, len(cols))
	for i := range cols {
		ph[i] = s.d.Placeholder(i + 1)
	}
	stmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(ph, ", "))
	_, err := s.db.ExecContext(ctx, stmt,
		log.EntityType, log.EntityID, log.Action,
		nullableJSON(log.OldValues), nullableJSON(log.NewValues),
		log.UserID, nullableString(log.UserType),
		nullableJSON(log.Metadata),
		nullableString(log.TransactionID),
		log.CreatedAt,
	)
	return err
}

func (s *sqlStore) SaveAPILog(ctx context.Context, table string, log entry.AuditAPILog) error {
	cols := []string{
		"service", "endpoint", "method", "status_code",
		"request_headers", "request_body", "response_body",
		"duration_ms", "error_message", "user_id",
		"metadata", "transaction_id", "created_at",
	}
	ph := make([]string, len(cols))
	for i := range cols {
		ph[i] = s.d.Placeholder(i + 1)
	}
	stmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(ph, ", "))
	_, err := s.db.ExecContext(ctx, stmt,
		log.Service, log.Endpoint, log.Method, log.StatusCode,
		nullableJSON(log.RequestHeaders), nullableJSON(log.RequestBody), nullableJSON(log.ResponseBody),
		log.DurationMs, nullableString(log.ErrorMessage),
		nullableString(log.UserID),
		nullableJSON(log.Metadata),
		nullableString(log.TransactionID),
		log.CreatedAt,
	)
	return err
}

func (s *sqlStore) QueryDataLogs(ctx context.Context, table string, f entry.DataFilter) ([]entry.AuditLog, error) {
	var where []string
	var args []any
	i := 1
	add := func(cond string, v any) {
		where = append(where, strings.Replace(cond, "?", s.d.Placeholder(i), 1))
		args = append(args, v)
		i++
	}
	if f.EntityType != "" {
		add("entity_type = ?", f.EntityType)
	}
	if f.EntityID != "" {
		add("entity_id = ?", f.EntityID)
	}
	if f.Action != "" {
		add("action = ?", f.Action)
	}
	if f.UserID != "" {
		add("user_id = ?", f.UserID)
	}
	if f.TransactionID != "" {
		add("transaction_id = ?", f.TransactionID)
	}
	if !f.DateFrom.IsZero() {
		add("created_at >= ?", f.DateFrom)
	}
	if !f.DateTo.IsZero() {
		add("created_at <= ?", f.DateTo)
	}

	q := fmt.Sprintf("SELECT id, entity_type, entity_id, action, old_values, new_values, user_id, user_type, metadata, transaction_id, created_at FROM %s", table)
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	if f.Offset > 0 {
		q += fmt.Sprintf(" OFFSET %d", f.Offset)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []entry.AuditLog
	for rows.Next() {
		var l entry.AuditLog
		var oldV, newV, meta sql.NullString
		var userType, txID sql.NullString
		var created time.Time
		if err := rows.Scan(&l.ID, &l.EntityType, &l.EntityID, &l.Action,
			&oldV, &newV, &l.UserID, &userType, &meta, &txID, &created); err != nil {
			return nil, err
		}
		if oldV.Valid {
			l.OldValues = []byte(oldV.String)
		}
		if newV.Valid {
			l.NewValues = []byte(newV.String)
		}
		if meta.Valid {
			l.Metadata = []byte(meta.String)
		}
		l.UserType = userType.String
		l.TransactionID = txID.String
		l.CreatedAt = created
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *sqlStore) QueryAPILogs(ctx context.Context, table string, f entry.APIFilter) ([]entry.AuditAPILog, error) {
	var where []string
	var args []any
	i := 1
	add := func(cond string, v any) {
		where = append(where, strings.Replace(cond, "?", s.d.Placeholder(i), 1))
		args = append(args, v)
		i++
	}
	if f.Service != "" {
		add("service = ?", f.Service)
	}
	if f.StatusCode != 0 {
		add("status_code = ?", f.StatusCode)
	}
	if f.UserID != "" {
		add("user_id = ?", f.UserID)
	}
	if f.TransactionID != "" {
		add("transaction_id = ?", f.TransactionID)
	}
	if !f.DateFrom.IsZero() {
		add("created_at >= ?", f.DateFrom)
	}
	if !f.DateTo.IsZero() {
		add("created_at <= ?", f.DateTo)
	}

	q := fmt.Sprintf("SELECT id, service, endpoint, method, status_code, request_headers, request_body, response_body, duration_ms, error_message, user_id, metadata, transaction_id, created_at FROM %s", table)
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	if f.Offset > 0 {
		q += fmt.Sprintf(" OFFSET %d", f.Offset)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []entry.AuditAPILog
	for rows.Next() {
		var l entry.AuditAPILog
		var reqH, reqB, respB, meta sql.NullString
		var errMsg, userID, txID sql.NullString
		var statusCode, duration sql.NullInt64
		var created time.Time
		if err := rows.Scan(&l.ID, &l.Service, &l.Endpoint, &l.Method,
			&statusCode, &reqH, &reqB, &respB,
			&duration, &errMsg, &userID, &meta, &txID, &created); err != nil {
			return nil, err
		}
		l.StatusCode = int(statusCode.Int64)
		l.DurationMs = int(duration.Int64)
		if reqH.Valid {
			l.RequestHeaders = []byte(reqH.String)
		}
		if reqB.Valid {
			l.RequestBody = []byte(reqB.String)
		}
		if respB.Valid {
			l.ResponseBody = []byte(respB.String)
		}
		if meta.Valid {
			l.Metadata = []byte(meta.String)
		}
		l.ErrorMessage = errMsg.String
		l.UserID = userID.String
		l.TransactionID = txID.String
		l.CreatedAt = created
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *sqlStore) Purge(ctx context.Context, table string, before time.Time) (int64, error) {
	if !identRE.MatchString(table) {
		return 0, fmt.Errorf("audit: invalid table %q", table)
	}
	stmt := fmt.Sprintf("DELETE FROM %s WHERE created_at < %s", table, s.d.Placeholder(1))
	res, err := s.db.ExecContext(ctx, stmt, before)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
