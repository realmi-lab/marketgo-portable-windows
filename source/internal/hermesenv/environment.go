// Package hermesenv owns the environment boundary shared by local Hermes
// execution and its host bridge. It does not provide an OS sandbox.
package hermesenv

import "strings"

// RestrictedDatabaseName reports database credentials, connection settings and
// local database paths that an explanation-only child must not inherit.
// Keep this policy independent of the transport used to launch the model.
func RestrictedDatabaseName(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	if name == "DB" || strings.HasPrefix(name, "DB_") || strings.HasSuffix(name, "_DB") ||
		strings.Contains(name, "DATABASE") || strings.Contains(name, "POSTGRES") ||
		strings.Contains(name, "SQLITE") || strings.Contains(name, "MYSQL") ||
		strings.Contains(name, "MARIADB") || strings.Contains(name, "MONGODB") ||
		strings.Contains(name, "REDIS") || strings.Contains(name, "CLICKHOUSE") {
		return true
	}
	// libpq accepts both connection credentials and filesystem-based credential
	// or service configuration. SSL client-key locations are sensitive as well.
	// Registry: https://www.postgresql.org/docs/18/libpq-envars.html
	if strings.HasPrefix(name, "PGSSL") || strings.HasPrefix(name, "PGGSS") {
		return true
	}
	switch name {
	case "PGHOST", "PGHOSTADDR", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD",
		"PGPASSFILE", "PGSERVICE", "PGSERVICEFILE", "PGOPTIONS", "PGAPPNAME",
		"PGCONNECT_TIMEOUT", "PGTARGETSESSIONATTRS", "PGCHANNELBINDING",
		"PGREQUIREAUTH", "PGKRBSRVNAME", "PGREALM", "PGSYSCONFDIR",
		"PGREQUIREPEER", "PGREQUIRESSL", "PGLOADBALANCEHOSTS",
		"PGMINPROTOCOLVERSION", "PGMAXPROTOCOLVERSION", "PGCLIENTENCODING",
		"PGDATESTYLE", "PGTZ", "PGGEQO", "PGLOCALEDIR",
		"ODBCINI", "ODBCSYSINI", "JDBC_URL":
		return true
	default:
		return false
	}
}
