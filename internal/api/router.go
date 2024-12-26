package api

import (
	"github.com/gin-gonic/gin"
)

func Router(r gin.IRouter) {

	apiRouter := r.Group("/api")
	apiRouter.GET("/home", Home)

}
