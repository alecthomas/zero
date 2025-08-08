package zero_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero"
)

type mockStatusCoder struct {
	Data string `json:"data"`
	Code int    `json:"code"`
}

func (m mockStatusCoder) StatusCode() int {
	return m.Code
}

type mockNamedReader struct {
	*strings.Reader
	name string
}

func (m mockNamedReader) Name() string {
	return m.name
}

type mockNamedReadCloser struct {
	*strings.Reader
	name string
}

func (m mockNamedReadCloser) Name() string {
	return m.name
}

func (m mockNamedReadCloser) Close() error {
	return nil
}

func TestEncodeResponse(t *testing.T) {
	logger := slog.Default()
	errorEncoder := zero.EncodeError

	tests := []struct {
		name           string
		data           any
		expectedStatus int
		expectedBody   string
		expectedHeader map[string]string
	}{
		{
			name:           "StringResponse",
			data:           "Hello World",
			expectedStatus: http.StatusOK,
			expectedBody:   "Hello World",
			expectedHeader: map[string]string{"Content-Type": "text/html"},
		},
		{
			name:           "ByteSliceResponse",
			data:           []byte("Binary Data"),
			expectedStatus: http.StatusOK,
			expectedBody:   "Binary Data",
			expectedHeader: map[string]string{"Content-Type": "application/octet-stream"},
		},
		{
			name:           "JSONResponse",
			data:           map[string]string{"message": "hello"},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"message":"hello"}` + "\n",
			expectedHeader: map[string]string{"Content-Type": "application/json; charset=utf-8"},
		},
		{
			name:           "StatusCoderString",
			data:           mockStatusCoder{Data: "Custom Status", Code: http.StatusCreated},
			expectedStatus: http.StatusCreated,
			expectedBody:   `{"data":"Custom Status","code":201}` + "\n",
			expectedHeader: map[string]string{"Content-Type": "application/json; charset=utf-8"},
		},
		{
			name:           "ReaderResponse",
			data:           strings.NewReader("Reader Content"),
			expectedStatus: http.StatusOK,
			expectedBody:   "Reader Content",
			expectedHeader: map[string]string{"Content-Type": "application/octet-stream"},
		},
		{
			name:           "NamedReaderResponse",
			data:           mockNamedReader{Reader: strings.NewReader("Named Reader"), name: "test.txt"},
			expectedStatus: http.StatusOK,
			expectedBody:   "Named Reader",
			expectedHeader: map[string]string{
				"Content-Type":        "application/octet-stream",
				"Content-Disposition": `attachment; filename="test.txt"`,
			},
		},
		{
			name:           "ReadCloserResponse",
			data:           io.NopCloser(strings.NewReader("ReadCloser Content")),
			expectedStatus: http.StatusOK,
			expectedBody:   "ReadCloser Content",
			expectedHeader: map[string]string{"Content-Type": "application/octet-stream"},
		},
		{
			name:           "NamedReadCloserResponse",
			data:           mockNamedReadCloser{Reader: strings.NewReader("Named ReadCloser"), name: "file.dat"},
			expectedStatus: http.StatusOK,
			expectedBody:   "Named ReadCloser",
			expectedHeader: map[string]string{
				"Content-Type":        "application/octet-stream",
				"Content-Disposition": `attachment; filename="file.dat"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)

			zero.EncodeResponse(logger, r, w, errorEncoder, tt.data, nil)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Equal(t, tt.expectedBody, w.Body.String())

			for key, expectedValue := range tt.expectedHeader {
				assert.Equal(t, expectedValue, w.Header().Get(key))
			}
		})
	}
}

func TestEncodeResponseHTTPResponse(t *testing.T) {
	logger := slog.Default()
	errorEncoder := zero.EncodeError

	// Create a mock HTTP response
	resp := &http.Response{
		StatusCode: http.StatusAccepted,
		Header: http.Header{
			"Custom-Header": []string{"custom-value"},
			"Content-Type":  []string{"application/xml"},
		},
		Body: io.NopCloser(strings.NewReader("<xml>content</xml>")),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	zero.EncodeResponse(logger, r, w, errorEncoder, resp, nil)

	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "<xml>content</xml>", w.Body.String())
	assert.Equal(t, "custom-value", w.Header().Get("Custom-Header"))
	assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
}

func TestEncodeResponseHTTPHandler(t *testing.T) {
	logger := slog.Default()
	errorEncoder := zero.EncodeError

	// Create a mock HTTP handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Handler-Header", "handler-value")
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("I'm a teapot"))
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	zero.EncodeResponse(logger, r, w, errorEncoder, handler, nil)

	assert.Equal(t, http.StatusTeapot, w.Code)
	assert.Equal(t, "I'm a teapot", w.Body.String())
	assert.Equal(t, "handler-value", w.Header().Get("Handler-Header"))
}

func TestEncodeResponseWithError(t *testing.T) {
	logger := slog.Default()
	errorEncoder := zero.EncodeError

	t.Run("RegularError", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)

		err := fmt.Errorf("something went wrong")
		zero.EncodeResponse(logger, r, w, errorEncoder, nil, err)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))

		var response map[string]string
		err = json.Unmarshal(w.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Equal(t, "something went wrong", response["error"])
		assert.Equal(t, "500", response["code"])
	})

	t.Run("HTTPHandlerError", func(t *testing.T) {
		// Create an error that implements http.Handler
		handlerError := zero.APIErrorf(http.StatusBadRequest, "bad request error")

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)

		zero.EncodeResponse(logger, r, w, errorEncoder, nil, handlerError)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))

		var response map[string]string
		err := json.Unmarshal(w.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Equal(t, "bad request error", response["error"])
		assert.Equal(t, "400", response["code"])
	})
}

func TestEncodeResponseNamedWithSpecialCharacters(t *testing.T) {
	logger := slog.Default()
	errorEncoder := zero.EncodeError

	// Test with filename that needs URL escaping
	namedReader := mockNamedReader{
		Reader: strings.NewReader("content"),
		name:   "file with spaces & symbols 🤔.txt",
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	zero.EncodeResponse(logger, r, w, errorEncoder, namedReader, nil)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "content", w.Body.String())
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename*=UTF-8''file+with+spaces+%26+symbols+%F0%9F%A4%94.txt`, w.Header().Get("Content-Disposition"))
}
