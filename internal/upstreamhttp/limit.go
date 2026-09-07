// Package upstreamhttp contains transport guards shared by external service
// integrations.
package upstreamhttp

import (
	"errors"
	"io"
	"net/http"
)

var ErrResponseTooLarge = errors.New("upstream response exceeds configured size limit")

// LimitResponses wraps base and enforces maxBytes against the decoded response
// body. Known oversized Content-Length values are rejected before a body read;
// streamed or compressed responses fail as soon as maxBytes+1 is observed.
func LimitResponses(base http.RoundTripper, maxBytes int64) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &responseLimitTransport{base: base, maxBytes: maxBytes}
}

type responseLimitTransport struct {
	base     http.RoundTripper
	maxBytes int64
}

func (transport *responseLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return response, nil
	}
	if transport.maxBytes < 1 || response.ContentLength > transport.maxBytes {
		_ = response.Body.Close()
		return nil, ErrResponseTooLarge
	}
	response.Body = &limitedReadCloser{ReadCloser: response.Body, remaining: transport.maxBytes}
	return response, nil
}

type limitedReadCloser struct {
	io.ReadCloser
	remaining int64
}

func (body *limitedReadCloser) Read(buffer []byte) (int, error) {
	if body.remaining == 0 {
		var probe [1]byte
		read, err := body.ReadCloser.Read(probe[:])
		if read > 0 {
			return 0, ErrResponseTooLarge
		}
		return 0, err
	}
	if int64(len(buffer)) > body.remaining {
		buffer = buffer[:body.remaining]
	}
	read, err := body.ReadCloser.Read(buffer)
	body.remaining -= int64(read)
	return read, err
}
