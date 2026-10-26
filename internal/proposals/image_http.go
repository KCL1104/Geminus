package proposals

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/proposals/photo"
	"github.com/kcl1104/geminus/internal/tenant"
)

// The HTTP adapter of createImageProposal (T-056): a multipart/form-data
// body whose parts base_version_id and, optionally, output_locale come
// first, each once, and file last. The caller's role, AI and object
// storage are checked before any of the body is read; the file is read
// once, at most photo.MaxBytes.

// imageOverhead is the room the fields and part headers may take beside
// the file in one request body.
const imageOverhead = 64 << 10

// imageTimeout bounds reading one photo's body and writing the response,
// in place of the server's shorter read timeout (5 MiB at about 0.35
// Mbit/s).
const imageTimeout = 2 * time.Minute

// maxImageField bounds a form field's value (the longest is an ID).
const maxImageField = 128

var imageFieldParts = map[string]bool{"base_version_id": true, "output_locale": true}

func malformed(message string) error {
	return &httpapi.Error{Code: httpapi.InvalidRequest, Message: message, Details: []httpapi.Detail{{Path: "", Reason: "multipart/form-data body"}}}
}

func fieldIssue(path, message string) error {
	return &RequestError{Issues: []model.ValidationIssue{model.NewIssue(validate.ConstraintFailed, path, message, model.SeverityError)}}
}

func partError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return photo.ErrTooLarge
	}
	return malformed("the multipart/form-data body is malformed")
}

// imageParts reads the fields up to the file part, which it returns.
func imageParts(mr *multipart.Reader) (ImageFields, *multipart.Part, error) {
	values := map[string]string{}
	for {
		p, err := mr.NextRawPart()
		if err == io.EOF {
			return ImageFields{}, nil, fieldIssue("/file", "the file part is missing")
		}
		if err != nil {
			return ImageFields{}, nil, partError(err)
		}
		d, params, err := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		if err != nil || d != "form-data" || params["name"] == "" {
			return ImageFields{}, nil, malformed("each part needs a Content-Disposition of form-data with a name")
		}
		if cte := strings.ToLower(p.Header.Get("Content-Transfer-Encoding")); cte != "" && cte != "binary" && cte != "8bit" && cte != "7bit" {
			return ImageFields{}, nil, malformed("parts are sent as they are, without a Content-Transfer-Encoding")
		}
		name := params["name"]
		_, isFile := params["filename"]
		if name == "file" {
			if _, ok := values["base_version_id"]; !ok {
				return ImageFields{}, nil, fieldIssue("/base_version_id", "base_version_id is required, before the file part")
			}
			// The file's name is never kept: the image is the request.
			return ImageFields{BaseVersionID: values["base_version_id"], OutputLocale: values["output_locale"]}, p, nil
		}
		if !imageFieldParts[name] {
			return ImageFields{}, nil, fieldIssue(validate.Pointer(name), "the request has no part of this name")
		}
		if isFile {
			return ImageFields{}, nil, malformed(name + " is a form field, not a file")
		}
		if _, dup := values[name]; dup {
			return ImageFields{}, nil, malformed(name + " is sent more than once")
		}
		v, err := io.ReadAll(io.LimitReader(p, maxImageField+1))
		if err != nil {
			return ImageFields{}, nil, partError(err)
		}
		if len(v) > maxImageField || !utf8.Valid(v) {
			return ImageFields{}, nil, fieldIssue("/"+name, name+" is not a valid value")
		}
		values[name] = string(v)
	}
}

func (h handlers) createImage(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	modelID, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" || params["boundary"] == "" {
		return httpapi.Errorf(httpapi.UnsupportedMediaType, "the request body must be multipart/form-data")
	}
	limit := int64(photo.MaxBytes + imageOverhead)
	if r.ContentLength > limit {
		return Refused(photo.ErrTooLarge)
	}
	// Who may ask, and whether a photo can be taken at all, before any of
	// the body is read.
	if err := h.s.BeginImage(r.Context(), sc); err != nil {
		return Refused(err)
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(imageTimeout))
	_ = rc.SetWriteDeadline(time.Now().Add(imageTimeout + 30*time.Second))
	mr := multipart.NewReader(http.MaxBytesReader(w, r.Body, limit), params["boundary"])
	f, file, err := imageParts(mr)
	if err != nil {
		return Refused(err)
	}
	data, err := photo.Read(file)
	if err != nil {
		if errors.Is(err, photo.ErrTooLarge) {
			return Refused(err)
		}
		return Refused(partError(err))
	}
	// The file is the last part.
	switch _, err := mr.NextRawPart(); {
	case err == nil:
		return malformed("the file part must be the last part")
	case err != io.EOF:
		return Refused(partError(err))
	}
	status, out, err := h.s.CreateFromImage(r.Context(), sc, httpapi.RequestID(r.Context()), key, modelID, f, data)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

// refusedImage maps the photo's own errors (photo, storage) to what the
// client sees; nil for any other error.
func refusedImage(err error) error {
	file := func(code httpapi.Code, message, reason, detail string) error {
		return &httpapi.Error{Code: code, Message: message, Details: []httpapi.Detail{{Path: "/file", Reason: reason, Code: detail}}}
	}
	switch {
	case errors.Is(err, photo.ErrFormat):
		return file(httpapi.UnsupportedMediaType, "a photo or floor plan is a PNG or a JPEG", "the file is not a PNG or a JPEG", CodeImageFormatRejected)
	case errors.Is(err, photo.ErrTooLarge):
		return file(httpapi.PayloadTooLarge, "the photo is larger than a proposal may take", "the file is more than 5,242,880 bytes (5 MiB)", "")
	case errors.Is(err, photo.ErrEmpty), errors.Is(err, photo.ErrInvalid):
		return file(httpapi.RequestValidationFailed, "the photo is not acceptable", "the file is empty or does not decode as its format", CodeImageInvalid)
	case errors.Is(err, photo.ErrPixels):
		return file(httpapi.RequestValidationFailed, "the photo is not acceptable", "the image is less than 32 or more than 4096 pixels wide or high", CodeImagePixels)
	case errors.Is(err, ErrStorageUnavailable):
		return httpapi.Errorf(httpapi.ServiceUnavailable, "object storage is not configured for this deployment")
	case errors.Is(err, ErrRetry):
		return httpapi.Errorf(httpapi.ServiceUnavailable, "the request could not be completed; retry it with the same Idempotency-Key")
	}
	return nil
}
