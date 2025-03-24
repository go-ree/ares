package util

import (
	"strings"

	"github.com/gin-gonic/gin"
)

type ParamPage struct {
	Page    int      `form:"page" binding:"required,min=1"`
	Perpage int      `form:"perpage" binding:"required,min=1,max=200"` // 前端可选 15 30 50 100 200
	Sort    []string `form:"sort" collection_format:"csv"`
}

func (p *ParamPage) GetSortSqlDemo(mapping map[string]string) string {
	if len(p.Sort) == 0 {
		return ""
	}

	var sortSql string
	for _, s := range p.Sort {
		if _, ok := mapping[s]; !ok {
			continue
		}
		if strings.HasPrefix(s, "-") {
			sortSql += mapping[s[1:]] + " DESC,"
		} else {
			sortSql += mapping[s] + " ASC,"
		}
	}

	if sortSql == "" {
		return ""
	}
	return sortSql[:len(sortSql)-1]
}

// ResponseError 定义了 API 响应的结构
// @Description API 响应结构
func ResponseError(err string) gin.H {
	return gin.H{
		"error": err,
	}
}

// ResponseSuccess 定义了 API 响应的结构
// @Description API 响应结构
func ResponseSuccess(data interface{}) gin.H {
	return gin.H{
		"data": data,
	}
}

// ResponsePage 定义了 API 响应的结构
// @Description API 响应结构
func ResponsePage(data interface{}, total int) gin.H {
	return gin.H{
		"total": total,
		"data":  data,
	}
}

// Response 定义了 API 响应的结构
// @Description API 响应结构
// 根据天天拍业务的处理逻辑，所有的响应都通过这个
func Response(code int, message string, result interface{}) gin.H {
	return gin.H{
		"code":    code,
		"message": message,
		"result":  result,
	}
}
