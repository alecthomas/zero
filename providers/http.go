package providers

import (
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
func DefaultErrorEncoder() zero.ErrorEncoder { return zero.EncodeError }

// DefaultResponseEncoder encodes responses using the default Zero format.
//
//zero:provider weak
func DefaultResponseEncoder() zero.ResponseEncoder { return zero.EncodeResponse }
