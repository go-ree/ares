package util

import (
	"fmt"
	"strconv"
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
	PageNum  interface{} `form:"page_num" json:"page_num" binding:"omitempty,min=1"`
	PageSize interface{} `form:"page_size" json:"page_size" binding:"omitempty,min=1,max=200"` // 前端可选 15 30 50 100 200
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

// GetPageNum 获取页码，兼容 string 和 int 类型
func (p *ParamPage) GetPageNum() int {
	if p.PageNum == nil {
		return 0
	}

	switch v := p.PageNum.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		if val, err := strconv.Atoi(v); err == nil {
			return val
		}
		return 0
	default:
		// 尝试转换为字符串再解析
		if str, ok := fmt.Sprintf("%v", v), true; ok {
			if val, err := strconv.Atoi(str); err == nil {
				return val
			}
		}
		return 0
	}
}

// GetPageSize 获取每页大小，兼容 string 和 int 类型
func (p *ParamPage) GetPageSize() int {
	if p.PageSize == nil {
		return 0
	}

	switch v := p.PageSize.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		if val, err := strconv.Atoi(v); err == nil {
			return val
		}
		return 0
	default:
		// 尝试转换为字符串再解析
		if str, ok := fmt.Sprintf("%v", v), true; ok {
			if val, err := strconv.Atoi(str); err == nil {
				return val
			}
		}
		return 0
	}
}

// SetPageNum 设置页码
func (p *ParamPage) SetPageNum(pageNum int) {
	p.PageNum = pageNum
}

// SetPageSize 设置每页大小
func (p *ParamPage) SetPageSize(pageSize int) {
	p.PageSize = pageSize
}

// NormalizePagination 规范化分页参数
func (p *ParamPage) NormalizePagination(params *ParamPage) (int, int) {
	// 获取页码和每页大小
	pageNum := params.GetPageNum()
	pageSize := params.GetPageSize()

	// 默认分页参数
	if pageNum <= 0 {
		pageNum = 1
		params.SetPageNum(pageNum)
	}
	if pageSize <= 0 {
		pageSize = 5
		params.SetPageSize(pageSize)
	}

	// 计算正确的偏移量
	offset := (pageNum - 1) * pageSize

	return pageSize, offset
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
	Code    int    `json:"code"`    //此处约定：1代表成功，0代表失败
	Message string `json:"message"` //对请求结果的描述消息，可以为空
	Result  any    `json:"result"`  //如果请求成功，这里给出成功的结果
	Error   any    `json:"error"`   //如果请求失败，这里一定要给出错误的信息
	Help    string `json:"help"`    //显示接口文档地址，便于别人排错
}

func ResponseSuccessful(message string, result any) ResponseTemplate {
	return ResponseTemplate{
		Code:    1,
		Message: message,
		Result:  result, //响应成功要把result附上
		Help:    "暂不提供帮助信息",
	}
}

func ResponseFailure(message string, error any) ResponseTemplate {
	return ResponseTemplate{
		Code:    0,
		Message: message,
		Error:   error, //响应失败要把error附上
		Help:    "暂不提供帮助信息",
	}
}
