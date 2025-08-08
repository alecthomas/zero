package providers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/alecthomas/zero"
)

// DefaultErrorEncoder for otherwise unhandled errors.
//
// The response will be JSON in the form:
//
//	{
//	  "error": "error message",
//	  "code": code
//	}
//
//zero:provider weak
func DefaultErrorEncoder() zero.ErrorEncoder {
	return func(w http.ResponseWriter, msg string, status int) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": strconv.Itoa(status)})
	}
}

// DefaultResponseEncoder encodes responses using the default Zero format.
//
//zero:provider weak
func DefaultResponseEncoder() zero.ResponseEncoder { return zero.EncodeResponse }
