package providers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/alecthomas/zero"
)

// DefaultErrorHandler for otherwise unhandled errors.
//
// The response will be JSON in the form:
//
//	{
//	  "error": "error message",
//	  "code": code
//	}
//
//zero:provider weak
func DefaultErrorHandler() zero.ErrorHandler {
	return func(w http.ResponseWriter, msg string, status int) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": strconv.Itoa(status)})
	}
}
