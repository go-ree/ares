package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
)

const defaultJSONRequestBytes int64 = 1024 * 1024

// BindJSON is the single JSON request boundary for Ares. It rejects ambiguous
// payloads, unknown fields and unbounded bodies before a domain handler runs.
// Error responses deliberately omit decoder details because a request may
// contain credentials or other values that must never be reflected.
func BindJSON(c *gin.Context, target any, maxBytes int64) bool {
	return bindJSON(c, target, maxBytes, "")
}

// BindCanonicalJSON keeps the same strict parser while exposing only stable
// machine codes to new APIs. Legacy handlers retain their historical text.
func BindCanonicalJSON(c *gin.Context, target any, maxBytes int64) bool {
	return bindJSON(c, target, maxBytes, "invalid_request")
}

func bindJSON(c *gin.Context, target any, maxBytes int64, errorCode string) bool {
	if maxBytes <= 0 {
		maxBytes = defaultJSONRequestBytes
	}
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
		if errorCode != "" {
			c.JSON(http.StatusUnsupportedMediaType, util.ResponseFailure("请求数据格式错误", errorCode))
			return false
		}
		c.JSON(http.StatusUnsupportedMediaType, util.ResponseFailure(
			"请求数据格式错误", "Content-Type 必须是 application/json",
		))
		return false
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeJSONDecodeError(c, err, errorCode)
		return false
	}
	if !utf8.Valid(payload) {
		writeJSONDecodeError(c, errors.New("invalid UTF-8 JSON payload"), errorCode)
		return false
	}
	if err := rejectDuplicateJSONKeys(payload); err != nil {
		writeJSONDecodeError(c, err, errorCode)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSONDecodeError(c, err, errorCode)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			writeJSONDecodeError(c, err, errorCode)
			return false
		}
		publicError := any("请求只能包含一个 JSON 值")
		if errorCode != "" {
			publicError = errorCode
		}
		c.JSON(http.StatusBadRequest, util.ResponseFailure("请求数据格式错误", publicError))
		return false
	}
	return true
}

func rejectDuplicateJSONKeys(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON object key")
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func writeJSONDecodeError(c *gin.Context, err error, errorCode string) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		publicError := any("请求体超过允许大小")
		if errorCode != "" {
			publicError = "request_too_large"
		}
		c.JSON(http.StatusRequestEntityTooLarge, util.ResponseFailure("请求数据过大", publicError))
		return
	}
	publicError := any("JSON 请求体无效")
	if errorCode != "" {
		publicError = errorCode
	}
	c.JSON(http.StatusBadRequest, util.ResponseFailure("请求数据格式错误", publicError))
}
