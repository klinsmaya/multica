package marketinstall

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyNativeError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{name: "timeout", err: fmt.Errorf("dial tcp: i/o timeout"), want: errAmbiguous},
		{name: "primary key", err: &pgconn.PgError{Code: "23505", ConstraintName: "agent_pkey"}, want: errPKExists},
		{name: "name", err: &pgconn.PgError{Code: "23505", ConstraintName: "agent_workspace_name_unique"}, want: errNameTaken},
		{name: "not null", err: &pgconn.PgError{Code: "23502", ConstraintName: "agent_name_not_null"}, want: errDefinite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyNative(tc.err); !errors.Is(got, tc.want) {
				t.Fatalf("got %v", got)
			}
		})
	}
}
