package security

import (
	"encoding/json"
	"net/http"
	"strings"

	apperrors "superset/internal/pkg/errors"
)

// DecodeBody decodes a request body that may be JSON or form-encoded.
// Superset's FAB endpoints accept both content types, so the replica does too.
func DecodeBody(r *http.Request, v any) error {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			return apperrors.BadRequest("Could not parse form body")
		}
		// Forms carry a JSON blob in the "data" field for nested payloads.
		if raw := r.PostFormValue("data"); raw != "" {
			if err := json.Unmarshal([]byte(raw), v); err != nil {
				return apperrors.BadRequest("Invalid JSON body: %s", err.Error())
			}
			return nil
		}
		return apperrors.BadRequest("Form bodies must carry a JSON 'data' field")
	}
	if r.Body == nil {
		return apperrors.BadRequest("Empty request body")
	}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return apperrors.BadRequest("Invalid JSON body: %s", err.Error())
	}
	return nil
}
