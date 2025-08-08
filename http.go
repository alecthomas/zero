package zero

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alecthomas/errors"
	"github.com/dyninc/qstring"
)

// ErrorEncoder represents a function for handling errors from Zero's generated code.
//
// A custom provider can override this.
type ErrorEncoder func(w http.ResponseWriter, msg string, code int)

// ResponseEncoder represents a function for encoding the response body into JSON and writing it to the response writer.
//
// A custom provider can override this.
type ResponseEncoder func(r *http.Request, w http.ResponseWriter, errorEncoder ErrorEncoder, data any, outErr error)

// Middleware is a convenience type for Zero middleware.
type Middleware func(http.Handler) http.Handler

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

// EncodeResponse encodes the response body into JSON and writes it to the response writer.
func EncodeResponse[T any](r *http.Request, w http.ResponseWriter, errorEncoder ErrorEncoder, data T, outErr error) {
	if outErr != nil {
		var handler http.Handler
		if errors.As(outErr, &handler) {
			handler.ServeHTTP(w, nil)
		} else {
			errorEncoder(w, outErr.Error(), http.StatusInternalServerError)
		}
	} else if handler, ok := any(data).(http.Handler); ok {
		handler.ServeHTTP(w, r)
	} else {
		statusCode := http.StatusOK
		statusCoder, ok := any(data).(StatusCode)
		if ok {
			statusCode = statusCoder.StatusCode()
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(data) //nolint
	}
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
