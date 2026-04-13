package sqlc_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	dbsqlc "github.com/golusoris/golusoris/db/sqlc"
	gerr "github.com/golusoris/golusoris/errors"
)

func TestMapError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   error
		code gerr.Code
	}{
		{"nil pass-through", nil, ""},
		{"no rows -> NotFound", pgx.ErrNoRows, gerr.CodeNotFound},
		{"unique violation -> Conflict", &pgconn.PgError{Code: "23505"}, gerr.CodeConflict},
		{"exclusion violation -> Conflict", &pgconn.PgError{Code: "23P01"}, gerr.CodeConflict},
		{"serialization -> Unavailable", &pgconn.PgError{Code: "40001"}, gerr.CodeUnavailable},
		{"deadlock -> Unavailable", &pgconn.PgError{Code: "40P01"}, gerr.CodeUnavailable},
		{"fk violation -> BadRequest", &pgconn.PgError{Code: "23503"}, gerr.CodeBadRequest},
		{"not null -> BadRequest", &pgconn.PgError{Code: "23502"}, gerr.CodeBadRequest},
		{"unmapped pg error pass-through", &pgconn.PgError{Code: "99999"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := dbsqlc.MapError(tc.in)
			if tc.in == nil {
				if out != nil {
					t.Fatalf("expected nil, got %v", out)
				}
				return
			}
			if tc.code == "" {
				if !errors.Is(out, tc.in) {
					t.Errorf("expected pass-through, got %v", out)
				}
				return
			}
			var ge *gerr.Error
			if !errors.As(out, &ge) {
				t.Fatalf("expected *gerr.Error, got %T", out)
			}
			if ge.Code != tc.code {
				t.Errorf("Code = %s, want %s", ge.Code, tc.code)
			}
		})
	}
}
