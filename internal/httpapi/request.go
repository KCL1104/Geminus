package httpapi

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

type requestIDKey struct{}

// RequestID returns the ID Handler gave this request ("" outside one).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// HandlerFunc is a handler that returns its error instead of writing it.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Handler is the outermost layer of every route: it gives the request a
// server-issued ID ("req_" + ULID, echoed in X-Request-ID; a client value
// is never used, since it would go into audit rows), recovers a panic as
// INTERNAL_ERROR, and writes a returned error as the error envelope.
func Handler(log *slog.Logger, next HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := ids.New("req")
		w.Header().Set("X-Request-ID", id)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.ErrorContext(r.Context(), "handler panicked", "request_id", id, "panic", fmt.Sprint(v))
				WriteError(w, r, log, errors.New("panic"))
			}
		}()
		if err := next(w, r); err != nil {
			WriteError(w, r, log, err)
		}
	})
}

// MaxBodyBytes bounds a JSON request body unless a route sets another limit.
const MaxBodyBytes = 1 << 20

// DecodeJSON reads the request body as application/json of at most max
// bytes, validates it against the schema ref (e.g.
// "http.schema.json#/$defs/CreateProject") and decodes it into dst. It
// returns the body as received (for an idempotency hash). Malformed JSON,
// duplicate member names and invalid UTF-8 are 400 INVALID_REQUEST; a
// schema failure is 422 REQUEST_VALIDATION_FAILED with one detail per
// failing JSON Pointer.
func DecodeJSON(r *http.Request, max int64, ref string, dst any) ([]byte, error) {
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || !utf8Charset(params) {
		return nil, Errorf(UnsupportedMediaType, "the request body must be application/json")
	}
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, max))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, Errorf(PayloadTooLarge, fmt.Sprintf("the request body is larger than %d bytes", max))
	}
	if err != nil {
		return nil, Errorf(InvalidRequest, "the request body could not be read")
	}
	issues, err := validate.Document(data, ref, string(RequestValidationFailed))
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		if issues[0].Code == validate.JSONInvalid {
			return nil, &Error{Code: InvalidRequest, Message: "the request body is not valid JSON",
				Details: []Detail{{Path: issues[0].Path, Reason: issues[0].Message}}}
		}
		e := &Error{Code: RequestValidationFailed, Message: "the request body does not match the schema"}
		for _, is := range issues {
			e.Details = append(e.Details, Detail{Path: is.Path, Reason: is.Message, Severity: string(is.Severity)})
		}
		return nil, e
	}
	// PostgreSQL text cannot hold U+0000, so a string carrying it is refused
	// here rather than failing in the database (T-009 review L1). NewIssue
	// cuts a long pointer to an ancestor that fits the schema's path limit,
	// as for a validator issue (re-review R2).
	if path, ok := nulString(data); ok {
		is := model.NewIssue(string(RequestValidationFailed), path, "strings may not contain U+0000", model.SeverityError)
		return nil, &Error{Code: RequestValidationFailed, Message: "the request body does not match the schema",
			Details: []Detail{{Path: is.Path, Reason: is.Message, Severity: string(is.Severity)}}}
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return nil, fmt.Errorf("schema-valid body does not decode: %w", err) // schema and type drifted
	}
	return data, nil
}

// nulString reports the JSON Pointer of the first string of data, a valid
// JSON document, that contains U+0000: the value's, or for a member name
// the object's.
func nulString(data []byte) (string, bool) {
	if !bytes.Contains(data, []byte(`\u0000`)) { // the only way to write U+0000 in JSON
		return "", false
	}
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return "", false
		}
		if tok.Kind() != '"' || !strings.ContainsRune(tok.String(), 0) {
			continue
		}
		p := dec.StackPointer()
		if kind, n := dec.StackIndex(dec.StackDepth()); kind == '{' && n%2 == 1 {
			p = p.Parent() // a member name
		}
		return string(p), true
	}
}

// Query parses the request's query string. Unlike r.URL.Query, it refuses
// a malformed one instead of dropping the pairs it cannot read (T-009
// review L6).
func Query(r *http.Request) (url.Values, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, &Error{Code: InvalidRequest, Message: "the query string is malformed",
			Details: []Detail{{Path: "", Reason: "query string"}}}
	}
	return q, nil
}

func utf8Charset(params map[string]string) bool {
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			return false
		}
	}
	return true
}
