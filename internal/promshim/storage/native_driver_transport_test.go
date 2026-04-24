package storage

import "testing"

func TestDriverParametersStripsHTTPParamPrefix(t *testing.T) {
	params := driverParameters(map[string]string{"param_evaluation_ms": "1", "plain": "two"})
	if params["evaluation_ms"] != "1" {
		t.Fatalf("evaluation_ms = %q, want 1", params["evaluation_ms"])
	}
	if params["plain"] != "two" {
		t.Fatalf("plain = %q, want two", params["plain"])
	}
	if _, ok := params["param_evaluation_ms"]; ok {
		t.Fatalf("driver parameters retained HTTP param_ prefix: %#v", params)
	}
}

func TestDriverSQLStripsJSONEachRowFormatOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
		want string
	}{
		{
			name: "format only suffix",
			sql:  "SELECT label FROM labels\nFORMAT JSONEachRow\n",
			want: "SELECT label FROM labels",
		},
		{
			name: "settings and format suffix",
			sql:  "SELECT value FROM samples\nSETTINGS allow_experimental_time_series_table = 1\nFORMAT JSONEachRow\n",
			want: "SELECT value FROM samples\nSETTINGS allow_experimental_time_series_table = 1",
		},
		{
			name: "no format suffix",
			sql:  "SELECT 1",
			want: "SELECT 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := driverSQL(tc.sql); got != tc.want {
				t.Fatalf("driverSQL() = %q, want %q", got, tc.want)
			}
		})
	}
}
