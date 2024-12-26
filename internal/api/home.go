package api

import (
	"net/http"

	"gitlab.ttpai.work/sre/pipeline/ares/internal/home"

	"github.com/gin-gonic/gin"
)

func Home(c *gin.Context) {
	home.Home()
	c.String(http.StatusOK, "Hello, World!")
}
