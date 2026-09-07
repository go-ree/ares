package upstreamhttp

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type trackingBody struct {
	io.Reader
	closed atomic.Bool
}

func (body *trackingBody) Close() error {
	body.closed.Store(true)
	return nil
}

func TestLimitResponsesRejectsKnownOversizeAndClosesBody(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader("unused")}
	transport := LimitResponses(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, ContentLength: 5, Body: body}, nil
	}), 4)

	response, err := transport.RoundTrip(&http.Request{})
	if response != nil || !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("RoundTrip() = (%#v, %v), want a size-limit error", response, err)
	}
	if !body.closed.Load() {
		t.Fatal("known oversized response body was not closed")
	}
}

func TestLimitResponsesRejectsStreamAfterMaxPlusOneAndClosesNormally(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader("12345")}
	transport := LimitResponses(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, ContentLength: -1, Body: body}, nil
	}), 4)

	response, err := transport.RoundTrip(&http.Request{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(response.Body)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("ReadAll() error = %v, want size-limit error", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if !body.closed.Load() {
		t.Fatal("wrapped response body did not delegate Close")
	}
}

func TestLimitResponsesAllowsExactBoundary(t *testing.T) {
	transport := LimitResponses(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, ContentLength: 4,
			Body: io.NopCloser(strings.NewReader("1234")),
		}, nil
	}), 4)
	response, err := transport.RoundTrip(&http.Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil || string(payload) != "1234" {
		t.Fatalf("ReadAll() = (%q, %v), want exact-boundary payload", payload, err)
	}
}
