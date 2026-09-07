package controller

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
)

func respondUpstreamFailure(c *gin.Context, integration, operation string, err error) {
	errorClass := "upstream_failure"
	var networkError net.Error
	switch {
	case errors.Is(err, context.Canceled):
		errorClass = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		errorClass = "timeout"
	case errors.As(err, &networkError) && networkError.Timeout():
		errorClass = "timeout"
	}
	slog.Error("upstream integration request failed",
		"request_id", RequestID(c),
		"integration", integration,
		"operation", operation,
		"error_class", errorClass,
	)
	c.JSON(http.StatusBadGateway, util.ResponseFailure("上游集成调用失败", "upstream integration unavailable"))
}
