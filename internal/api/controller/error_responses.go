package controller

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
)

const publicInternalServiceError = "internal service unavailable"

// writeInternalFailure keeps infrastructure and upstream implementation
// details behind the HTTP boundary. In particular, database driver errors may
// contain SQL text, host names or connection credentials and must never be
// copied into either an API response or an application log.
func writeInternalFailure(c *gin.Context, status int, message, component, operation string, err error) {
	errorClass := "internal_failure"
	switch {
	case errors.Is(err, context.Canceled):
		errorClass = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		errorClass = "timeout"
	}
	slog.Error("controller operation failed",
		"request_id", RequestID(c),
		"component", component,
		"operation", operation,
		"error_class", errorClass,
	)
	c.JSON(status, util.ResponseFailure(message, publicInternalServiceError))
}
