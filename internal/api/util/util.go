package util

import (
	"fmt"
	"strings"
)

// ParamPage 排序
//
//	{
//	   "page_num": 1,
//	   "page_size": 10,
//	   "sort": {
//	       "field": "created_at",
//	       "direction": "desc"
//	   }
//	}
type ParamPage struct {
	PageNum  int         `form:"page_num" json:"page_num" binding:"omitempty,min=1"`
	PageSize int         `form:"page_size" json:"page_size" binding:"omitempty,min=1,max=200"` // 前端可选 15 30 50 100 200
	Sort     *SortOption `form:"sort" collection_format:"csv"`
}

// SortOption 排序选项
type SortOption struct {
	Field     string `json:"field"`     // 排序字段
	Direction string `json:"direction"` // 排序方向：asc/desc
}

func NewUtilManager() *ParamPage {
	return &ParamPage{}
}

// NormalizePagination 规范化分页参数
func (p *ParamPage) NormalizePagination(params *ParamPage) (int, int) {
	// 默认分页参数
	if params.PageNum <= 0 {
		params.PageNum = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 5
	}

	// 计算正确的偏移量
	offset := (params.PageNum - 1) * params.PageSize

	return params.PageSize, offset
}

// CalculateTotalPages 计算总页数
func (p *ParamPage) CalculateTotalPages(total int64, pageSize int) int {
	return (int(total) + pageSize - 1) / pageSize
}

func (p *ParamPage) GetSortSqlDemo(mapping map[string]string) string {
	if p.Sort == nil {
		return ""
	}

	// 检查字段是否在允许的映射中
	field, ok := mapping[p.Sort.Field]
	if !ok {
		return ""
	}

	// 确定排序方向
	direction := "ASC"
	if strings.ToLower(p.Sort.Direction) == "desc" {
		direction = "DESC"
	}

	return fmt.Sprintf("%s %s", field, direction)
}

type ResponseTemplate struct {
	Code   int    `json:"code"`   //此处约定：1代表成功，0代表失败
	Msg    string `json:"msg"`    //对请求结果的描述消息，可以为空
	Result any    `json:"result"` //如果请求成功，这里给出成功的结果
	Error  any    `json:"error"`  //如果请求失败，这里一定要给出错误的信息
	Help   string `json:"help"`   //显示接口文档地址，便于别人排错
}

func ResponseSuccessful(msg string, result any) ResponseTemplate {
	return ResponseTemplate{
		Code:   1,
		Msg:    msg,
		Result: result, //响应成功要把result附上
		Help:   "暂不提供帮助信息",
	}
}

func ResponseFailure(msg string, error any) ResponseTemplate {
	return ResponseTemplate{
		Code:  0,
		Msg:   msg,
		Error: error, //响应失败要把error附上
		Help:  "暂不提供帮助信息",
	}
}
