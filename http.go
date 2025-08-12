package zero

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/alecthomas/errors"
	"github.com/dyninc/qstring"
)

// ErrorEncoder represents a function for handling errors from Zero's generated code.
//
// A custom provider can override this.
type ErrorEncoder func(logger *slog.Logger, w http.ResponseWriter, msg string, code int)

// ResponseEncoder represents a function for encoding the response body into JSON and writing it to the response writer.
//
// A custom provider can override this.
type ResponseEncoder func(logger *slog.Logger, r *http.Request, w http.ResponseWriter, errorEncoder ErrorEncoder, data any, outErr error)

// Middleware is a convenience type for Zero middleware.
type Middleware func(next http.Handler) http.Handler

// An APIError is an error that is also a http.Handler used to encode the error.
//
// Any request handler returning an error
type APIError interface {
	error
	http.Handler
}

// StatusCode is an interface that can be implemented by response types to provide a custom status code.
type StatusCode interface {
	StatusCode() int
}

// APIErrorf can be used with HTTP handlers to return a JSON-encoded error body in the form {"error: <msg>", "code": <code>}
func APIErrorf(code int, format string, args ...any) APIError {
	return apiError{
		code: code,
		err:  errors.Errorf(format, args...),
	}
}

type apiError struct {
	code int
	err  error
}

// Error implements APIError.
func (a apiError) Error() string { return fmt.Sprintf("%d: %s", a.code, a.err) }
func (a apiError) Unwrap() error { return a.err }

// ServeHTTP implements APIError.
func (a apiError) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(a.code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": a.err.Error(), "code": strconv.Itoa(a.code)}) //nolint
}

// DecodeRequest decodes the JSON request body into T for PATCH/POST/PUT methods, and query parameters for all other method types.
func DecodeRequest[T any](method string, r *http.Request) (T, error) {
	var result T
	method = strings.ToUpper(method)
	if method == http.MethodPatch || method == http.MethodPost || method == http.MethodPut {
		if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
			return result, APIErrorf(http.StatusBadRequest, "failed to decode JSON request body: %w", err)
		}
	} else if err := qstring.Unmarshal(r.URL.Query(), &result); err != nil {
		return result, APIErrorf(http.StatusBadRequest, "failed to decode query parameters: %w", err)
	}
	return result, nil
}

// EncodeError is the default error encoder.
//
// The response will be JSON in the form:
//
//	{
//	  "error": "error message",
//	  "code": code
//	}
func EncodeError(logger *slog.Logger, w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	eerr := json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": strconv.Itoa(status)})
	if eerr != nil {
		logger.Error("Failed to encode error", "error", msg, "status", status)
	}
}

// EncodeResponse encodes the response body into JSON and writes it to the response writer.
func EncodeResponse(logger *slog.Logger, r *http.Request, w http.ResponseWriter, errorEncoder ErrorEncoder, data any, outErr error) {
	if outErr != nil {
		var handler http.Handler
		if errors.As(outErr, &handler) {
			handler.ServeHTTP(w, nil)
		} else {
			errorEncoder(logger, w, outErr.Error(), http.StatusInternalServerError)
		}
		return
	}
	statusCode := http.StatusOK
	statusCoder, ok := data.(StatusCode)
	if ok {
		statusCode = statusCoder.StatusCode()
	}

	switch data := data.(type) {
	case http.Handler:
		data.ServeHTTP(w, r)

	case string:
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(statusCode)
		_, err := w.Write([]byte(data))
		if err != nil {
			logger.Error("Failed to write response", "error", err)
			return
		}

	case []byte:
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(statusCode)
		_, err := w.Write(data)
		if err != nil {
			logger.Error("Failed to write response", "error", err)
			return
		}

	case io.ReadCloser:
		defer func() {
			err := data.Close()
			if err != nil {
				logger.Error("Failed to close response", "error", err)
				return
			}
		}()
		w.Header().Set("Content-Type", "application/octet-stream")
		if named, ok := data.(interface{ Name() string }); ok {
			w.Header().Set("Content-Disposition", "attachment; "+contentDispositionFilename(named.Name()))
		}
		w.WriteHeader(statusCode)
		_, err := io.Copy(w, data)
		if err != nil {
			logger.Error("Failed to write response", "error", err)
			return
		}

	case io.Reader:
		w.Header().Set("Content-Type", "application/octet-stream")
		if named, ok := data.(interface{ Name() string }); ok {
			w.Header().Set("Content-Disposition", "attachment; "+contentDispositionFilename(named.Name()))
		}
		w.WriteHeader(statusCode)
		_, err := io.Copy(w, data)
		if err != nil {
			logger.Error("Failed to write response", "error", err)
			return
		}

	case *http.Response:
		defer func() {
			err := data.Body.Close()
			if err != nil {
				logger.Error("Failed to close response", "error", err)
				return
			}
		}()
		maps.Copy(w.Header(), data.Header)
		w.WriteHeader(data.StatusCode)
		_, err := io.Copy(w, data.Body)
		if err != nil {
			logger.Error("Failed to write response", "error", err)
			return
		}

	default:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(statusCode)
		err := json.NewEncoder(w).Encode(data) //nolint
		if err != nil {
			logger.Error("Failed to encode response", "error", err)
		}
	}
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}

func contentDispositionFilename(name string) string {
	if isASCII(name) {
		return `filename="` + escapeQuotes(name) + `"`
	}
	return "filename*=UTF-8''" + url.QueryEscape(name)
}

// EmptyResponse is used for handlers that don't return any content.
//
// It will write an empty response with a status code based on the HTTP method used:
//
//   - POST: StatusCreated
//   - PUT: StatusAccepted
//   - PATCH: StatusAccepted
//   - Other: StatusOK
type EmptyResponse []byte

func (e EmptyResponse) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		w.WriteHeader(http.StatusCreated)
	case http.MethodPut:
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPatch:
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusOK)
	}
}
