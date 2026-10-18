package ai

import "testing"

func TestValidateReadOnlySQL(t *testing.T) {
	allowed := []string{
		"SELECT 1",
		"select count(*) from birth_names",
		"WITH x AS (SELECT 1 AS a) SELECT a FROM x",
		// REPLACE is a scalar function here, not the write statement.
		"SELECT REPLACE(gender, 'girl', 'f') FROM birth_names",
		// Keywords inside literals are not structural.
		"SELECT 'delete from t' AS note",
	}
	for _, stmt := range allowed {
		if err := ValidateReadOnlySQL(stmt); err != nil {
			t.Fatalf("ValidateReadOnlySQL(%q) = %v, want nil", stmt, err)
		}
	}

	rejected := []string{
		"DROP TABLE ab_user",
		"DELETE FROM ab_user",
		"SELECT 1; DROP TABLE ab_user",
		"UPDATE ab_user SET is_active = true",
		"SELECT * INTO new_table FROM birth_names",
		"  ",
	}
	for _, stmt := range rejected {
		if err := ValidateReadOnlySQL(stmt); err == nil {
			t.Fatalf("ValidateReadOnlySQL(%q) = nil, want rejection", stmt)
		}
	}
}
