package audit

import (
	"errors"
	"fmt"
	"regexp"
)

// Config controls auditor behavior. Zero values are valid where noted; see
// defaults applied by New().
type Config struct {
	Dialect   DialectType
	UserFunc  UserFunc
	DataAudit DataAuditConfig
	APIAudit  APIAuditConfig

	// Logger is invoked when a storage error is swallowed under
	// ErrorFailSilent. nil falls back to log.Printf.
	Logger func(format string, args ...any)
}

// ErrorMode controls how the auditor reports storage failures.
type ErrorMode int

const (
	// ErrorFailLoud surfaces store errors back to the caller. Default —
	// in the GORM adapter the error is attached to the *gorm.DB via
	// AddError so callers see it via db.Error. This is also the mode to
	// use for compliance-strict deployments.
	ErrorFailLoud ErrorMode = iota

	// ErrorFailSilent logs store errors via Config.Logger and returns nil.
	// Use when the application write must not be coupled to audit
	// availability (e.g. degraded mode during audit DB outage).
	ErrorFailSilent
)

// DataAuditConfig controls data-change auditing.
type DataAuditConfig struct {
	Table           string
	Enabled         bool
	ExcludeFields   []string
	ExcludeEntities []string

	// SkipOldValues, when true, tells adapters not to snapshot pre-change
	// values on UPDATE/DELETE. old_values will be empty in the resulting log.
	// Trades audit completeness for one fewer SELECT per write.
	SkipOldValues bool

	// OnError controls how storage failures are reported. See ErrorMode.
	OnError ErrorMode
}

// APIAuditConfig controls third-party API call auditing.
type APIAuditConfig struct {
	Table            string
	Enabled          bool
	RedactHeaders    []string
	RedactBodyFields []string
	MaxBodySize      int

	// OnError controls how storage failures are reported. See ErrorMode.
	OnError ErrorMode
}

const (
	defaultDataTable   = "audit_logs"
	defaultAPITable    = "audit_api_logs"
	defaultMaxBodySize = 4096
	redactedMarker     = "***REDACTED***"
)

// identRE limits table names to SQL identifiers the core package is willing
// to interpolate directly into DDL / DML. Rejects anything that could open a
// second statement or close-quote the identifier.
var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

func (c *Config) applyDefaults() {
	if c.DataAudit.Table == "" {
		c.DataAudit.Table = defaultDataTable
	}
	if c.APIAudit.Table == "" {
		c.APIAudit.Table = defaultAPITable
	}
	if c.APIAudit.MaxBodySize == 0 {
		c.APIAudit.MaxBodySize = defaultMaxBodySize
	}
}

// validate is called by New / NewWithStore after defaults are applied.
func (c *Config) validate() error {
	if !c.DataAudit.Enabled && !c.APIAudit.Enabled {
		return errors.New("audit: at least one of DataAudit.Enabled / APIAudit.Enabled must be true")
	}
	if c.DataAudit.Enabled {
		if c.UserFunc == nil {
			return errors.New("audit: DataAudit.Enabled requires Config.UserFunc to be set")
		}
		if !identRE.MatchString(c.DataAudit.Table) {
			return fmt.Errorf("audit: invalid DataAudit.Table %q (must match %s)", c.DataAudit.Table, identRE)
		}
	}
	if c.APIAudit.Enabled {
		if !identRE.MatchString(c.APIAudit.Table) {
			return fmt.Errorf("audit: invalid APIAudit.Table %q (must match %s)", c.APIAudit.Table, identRE)
		}
	}
	return nil
}
