package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type stagedReadCloser struct {
	data []byte
	err  error
	done bool
}

func (s *stagedReadCloser) Read(dst []byte) (int, error) {
	if s.done {
		return 0, s.err
	}
	s.done = true
	n := copy(dst, s.data)
	return n, s.err
}

func (s *stagedReadCloser) Close() error {
	return nil
}

func TestPostJSONWithTimeoutPreservesConnectError(t *testing.T) {
	t.Parallel()

	expected := errors.New("dial tcp 127.0.0.1:50052: connectex: actively refused")
	client := &Client{
		baseURL: "http://127.0.0.1:50052",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return nil, expected
			}),
		},
	}

	err := client.postJSONWithTimeout(context.Background(), "/api/analyze_image", map[string]any{"foo": "bar"}, &GenericResponse{}, 0)
	if !errors.Is(err, expected) {
		t.Fatalf("expected connect error to be preserved, got %v", err)
	}
}

func TestPostJSONWithTimeoutReturnsReadErrorForPartialBody(t *testing.T) {
	t.Parallel()

	client := &Client{
		baseURL: "http://127.0.0.1:50052",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Body: &stagedReadCloser{
						data: []byte(`{"success":true`),
						err:  io.ErrUnexpectedEOF,
					},
				}, nil
			}),
		},
	}

	err := client.postJSONWithTimeout(context.Background(), "/api/analyze_image", map[string]any{"foo": "bar"}, &GenericResponse{}, 0)
	if err == nil {
		t.Fatal("expected read error")
	}
	if !strings.Contains(err.Error(), "read ai response failed") {
		t.Fatalf("expected read ai response failed, got %v", err)
	}
	if strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Fatalf("expected read error, got misleading decode error: %v", err)
	}
}

func TestPostJSONWithTimeoutKeepsHTTPErrorBody(t *testing.T) {
	t.Parallel()

	client := &Client{
		baseURL: "http://127.0.0.1:50052",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 500,
					Body:       io.NopCloser(strings.NewReader(`{"success":false,"message":"boom"}`)),
				}, nil
			}),
		},
	}

	err := client.postJSONWithTimeout(context.Background(), "/api/analyze_image", map[string]any{"foo": "bar"}, &GenericResponse{}, 0)
	if err == nil {
		t.Fatal("expected http error")
	}
	if !strings.Contains(err.Error(), "ai request failed [500]") {
		t.Fatalf("expected status error, got %v", err)
	}
}
