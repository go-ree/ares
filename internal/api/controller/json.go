package controller

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
)

const defaultJSONRequestBytes int64 = 1024 * 1024

// BindJSON is the single JSON request boundary for Ares. It rejects ambiguous
// payloads, unknown fields and unbounded bodies before a domain handler runs.
// Error responses deliberately omit decoder details because a request may
// contain credentials or other values that must never be reflected.
func BindJSON(c *gin.Context, target any, maxBytes int64) bool {
	if maxBytes <= 0 {
		maxBytes = defaultJSONRequestBytes
	}
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
		c.JSON(http.StatusUnsupportedMediaType, util.ResponseFailure(
			"请求数据格式错误", "Content-Type 必须是 application/json",
		))
		return false
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSONDecodeError(c, err)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			writeJSONDecodeError(c, err)
			return false
		}
		c.JSON(http.StatusBadRequest, util.ResponseFailure(
			"请求数据格式错误", "请求只能包含一个 JSON 值",
		))
		return false
	}
	return true
}

func writeJSONDecodeError(c *gin.Context, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		c.JSON(http.StatusRequestEntityTooLarge, util.ResponseFailure("请求数据过大", "请求体超过允许大小"))
		return
	}
	c.JSON(http.StatusBadRequest, util.ResponseFailure("请求数据格式错误", "JSON 请求体无效"))
}
