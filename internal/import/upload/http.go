package upload

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Route is one implemented operation of openapi.yaml.
type Route struct {
	Method, Path, OperationID string
	Roles                     []tenant.Role // x-required-roles
}

// Routes are the import operations this package serves (T-016, T-017). A
// contract test compares them with planning/api-operations.json. The
// suggestion and commit operations are T-027's and T-018's.
var Routes = []Route{
	{"POST", "/v1/imports", "uploadImport", writeRoles},
	{"GET", "/v1/imports/{id}", "getImport", readRoles},
	{"GET", "/v1/imports/{id}/errors", "downloadImportErrors", readRoles},
	{"POST", "/v1/imports/{id}/mapping", "saveImportMapping", writeRoles},
}

// Options are the HTTP adapter's (zero values: the defaults).
type Options struct {
	// UploadTimeout bounds reading one upload's body and writing its
	// response, in place of the server's shorter read timeout (5 min: 50 MB
	// at about 1.4 Mbit/s).
	UploadTimeout time.Duration
}

// Register adds the import routes to mux.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s *Service, opts Options) {
	if opts.UploadTimeout <= 0 {
		opts.UploadTimeout = 5 * time.Minute
	}
	h := handlers{s: s, opts: opts}
	for _, r := range Routes {
		var f func(http.ResponseWriter, *http.Request, tenant.Scope) error
		switch r.OperationID {
		case "uploadImport":
			f = h.upload
		case "getImport":
			f = h.get
		case "downloadImportErrors":
			f = h.report
		case "saveImportMapping":
			f = h.saveMapping
		}
		mux.Handle(r.Method+" "+r.Path, httpapi.Handler(log, httpapi.Scoped(v, users, f)))
	}
}

type handlers struct {
	s    *Service
	opts Options
}

// coded is issues as details that carry each issue's code (T-017).
func coded(issues []model.ValidationIssue) []httpapi.Detail {
	out := make([]httpapi.Detail, len(issues))
	for i, is := range issues {
		out[i] = httpapi.Detail{Path: is.Path, Reason: is.Message, Code: is.Code, Severity: string(is.Severity)}
	}
	return out
}

func details(issues []model.ValidationIssue) []httpapi.Detail {
	out := make([]httpapi.Detail, len(issues))
	for i, is := range issues {
		out[i] = httpapi.Detail{Path: is.Path, Reason: is.Message, Severity: string(is.Severity)}
	}
	return out
}

// Refused maps the service's errors to what the client sees (openapi error
// codes); tenant and transport errors pass to httpapi.
func Refused(err error) error {
	var re *RequestError
	var me *MappingError
	var ce *ConflictError
	var fe *FormatError
	var tooLarge *http.MaxBytesError
	var se *SourceError
	switch {
	case errors.As(err, &re):
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: "the upload is not acceptable", Details: details(re.Issues)}
	case errors.As(err, &me):
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: "the mapping is not acceptable", Details: coded(me.Issues)}
	case errors.As(err, &ce):
		return &httpapi.Error{Code: httpapi.LifecycleConflict, Message: ce.Message,
			Details: []httpapi.Detail{{Path: ce.Path, Reason: ce.Reason, Code: ce.Code}}}
	case errors.As(err, &fe):
		return &httpapi.Error{Code: httpapi.UnsupportedMediaType, Message: "archives and executables cannot be imported: upload CSV or JSON",
			Details: []httpapi.Detail{{Path: "/file", Reason: "the file is " + fe.Format, Code: FormatRejected}}}
	case errors.Is(err, ErrTooLarge), errors.As(err, &tooLarge):
		return &httpapi.Error{Code: httpapi.PayloadTooLarge, Message: "the file is larger than an import may be",
			Details: []httpapi.Detail{{Path: "/file", Reason: "the file is larger than this deployment accepts"}}}
	case errors.As(err, &se):
		return malformed("the request body could not be read")
	case errors.Is(err, ErrNotFound):
		return httpapi.Errorf(httpapi.NotFound, "not found")
	case errors.Is(err, ErrNoReport):
		return httpapi.Errorf(httpapi.NotFound, "the import has no error report")
	case errors.Is(err, ErrStorageUnavailable):
		return httpapi.Errorf(httpapi.ServiceUnavailable, "object storage is not configured for this deployment")
	case errors.Is(err, ErrRetry):
		return httpapi.Errorf(httpapi.ServiceUnavailable, "the request could not be completed; retry it with the same Idempotency-Key")
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return httpapi.Errorf(httpapi.IdempotencyKeyConflict, "this Idempotency-Key was used for a different request")
	}
	return err // ErrReportCorrupt and the rest: INTERNAL_ERROR, logged
}

func malformed(message string) error {
	return &httpapi.Error{Code: httpapi.InvalidRequest, Message: message, Details: []httpapi.Detail{{Path: "", Reason: "multipart/form-data body"}}}
}

// fieldParts are the form fields an upload sends before its file part.
var fieldParts = map[string]bool{"project_id": true, "model_id": true, "base_version_id": true, "kind": true}

// maxField bounds a form field's value (the longest is an ID).
const maxField = 128

// disposition is a part's Content-Disposition: form-data, its name and,
// for the file, its file name (RFC 7578; RFC 2231 encoded names too).
func disposition(p *multipart.Part) (name, filename string, hasFile bool, err error) {
	d, params, err := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
	if err != nil || d != "form-data" || params["name"] == "" {
		return "", "", false, malformed("each part needs a Content-Disposition of form-data with a name")
	}
	filename, hasFile = params["filename"]
	if cte := strings.ToLower(p.Header.Get("Content-Transfer-Encoding")); cte != "" && cte != "binary" && cte != "8bit" && cte != "7bit" {
		return "", "", false, malformed("parts are sent as they are, without a Content-Transfer-Encoding")
	}
	return params["name"], filename, hasFile, nil
}

// cleanName is the file name kept for an upload: the part after any
// directory (either separator), 1–255 characters of valid UTF-8 without
// control or format characters (which could disguise the name).
func cleanName(name string) (string, error) {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	n := utf8.RuneCountInString(name)
	bad := !utf8.ValidString(name) || n == 0 || n > 255 || name == "." || name == ".." ||
		strings.ContainsFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
	if bad {
		return "", invalid("/file", "the file part needs a file name of 1 to 255 characters without control or format characters")
	}
	return name, nil
}

// fields reads the form fields up to the file part, which it returns.
func fields(mr *multipart.Reader) (Fields, *multipart.Part, error) {
	values := map[string]string{}
	for {
		p, err := mr.NextRawPart()
		if err == io.EOF {
			return Fields{}, nil, invalid("/file", "the file part is missing")
		}
		if err != nil {
			return Fields{}, nil, partError(err)
		}
		name, filename, hasFile, err := disposition(p)
		if err != nil {
			return Fields{}, nil, err
		}
		if name == "file" {
			if !hasFile {
				return Fields{}, nil, invalid("/file", "the file part needs a file name")
			}
			var missing []model.ValidationIssue
			for _, f := range []string{"project_id", "model_id", "base_version_id", "kind"} {
				if _, ok := values[f]; !ok {
					missing = append(missing, model.NewIssue(validate.ConstraintFailed, "/"+f, f+" is required, before the file part", model.SeverityError))
				}
			}
			if len(missing) > 0 {
				return Fields{}, nil, &RequestError{Issues: missing}
			}
			clean, err := cleanName(filename)
			if err != nil {
				return Fields{}, nil, err
			}
			f := Fields{ProjectID: values["project_id"], ModelID: values["model_id"], BaseVersionID: values["base_version_id"],
				Kind: values["kind"], Filename: clean}
			if issues := f.Check(); len(issues) > 0 {
				return Fields{}, nil, &RequestError{Issues: issues}
			}
			return f, p, nil
		}
		if !fieldParts[name] {
			return Fields{}, nil, invalid(validate.Pointer(name), "the upload has no part of this name")
		}
		if hasFile {
			return Fields{}, nil, malformed(name + " is a form field, not a file")
		}
		if _, dup := values[name]; dup {
			return Fields{}, nil, malformed(name + " is sent more than once")
		}
		v, err := io.ReadAll(io.LimitReader(p, maxField+1))
		if err != nil {
			return Fields{}, nil, partError(err)
		}
		if len(v) > maxField || !utf8.Valid(v) {
			return Fields{}, nil, invalid("/"+name, name+" is not a valid value")
		}
		values[name] = string(v)
	}
}

func partError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return err
	}
	return malformed("the multipart/form-data body is malformed")
}

// uploadOverhead is the room the form fields and part headers may take
// beside the file in one request body.
const uploadOverhead = 64 << 10

func (h handlers) upload(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" || params["boundary"] == "" {
		return httpapi.Errorf(httpapi.UnsupportedMediaType, "the request body must be multipart/form-data")
	}
	limit := h.s.maxBytes() + uploadOverhead
	if r.ContentLength > limit {
		return Refused(ErrTooLarge)
	}
	// Who may upload is checked before any of the body is read.
	if err := h.s.Begin(r.Context(), sc); err != nil {
		return Refused(err)
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(h.opts.UploadTimeout))
	_ = rc.SetWriteDeadline(time.Now().Add(h.opts.UploadTimeout + 30*time.Second))
	mr := multipart.NewReader(http.MaxBytesReader(w, r.Body, limit), params["boundary"])
	f, file, err := fields(mr)
	if err != nil {
		return Refused(err)
	}
	received, err := h.s.Receive(r.Context(), sc, key, f, file)
	if err != nil {
		return Refused(err)
	}
	// The file is the last part.
	switch _, err := mr.NextRawPart(); {
	case err == nil:
		return malformed("the file part must be the last part")
	case err != io.EOF:
		return Refused(partError(err))
	}
	status, out, err := h.s.Commit(r.Context(), sc, httpapi.RequestID(r.Context()), key, f, received)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

// pathID is the {id} of the request if it is an import ID, else 400.
func pathID(r *http.Request) (string, error) {
	id := r.PathValue("id")
	if !importIDs.MatchString(id) {
		return "", &httpapi.Error{Code: httpapi.InvalidRequest, Message: "id must be an import ID",
			Details: []httpapi.Detail{{Path: "", Reason: "id"}}}
	}
	return id, nil
}

func (h handlers) get(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	imp, err := h.s.Get(r.Context(), sc, httpapi.RequestID(r.Context()), id)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteJSON(w, 200, imp)
	return nil
}

// saveMapping is saveImportMapping (T-017): the Idempotency-Key, then the
// body (SaveMapping), then the service.
func (h handlers) saveMapping(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req SaveRequest
	body, err := httpapi.DecodeJSON(r, httpapi.MaxBodyBytes, "http.schema.json#/$defs/SaveMapping", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.SaveMapping(r.Context(), sc, httpapi.RequestID(r.Context()), key, id, body, req)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

// report serves the error report itself, after the service has authorized
// the caller and checked the stored bytes (D-044: no signed URL), as a CSV
// attachment named only by the import's ID.
func (h handlers) report(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	data, err := h.s.ErrorReport(r.Context(), sc, id)
	if err != nil {
		return Refused(err)
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`-errors.csv"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	_, _ = w.Write(data)
	return nil
}
