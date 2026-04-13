package pgx

import gerr "github.com/golusoris/golusoris/errors"

// errMissingDSN is returned when Options.DSN is empty. Exposed via Is-friendly
// sentinel so tests can assert on it without string matching.
var errMissingDSN = gerr.New(gerr.CodeBadRequest, "db/pgx: DSN is required")
