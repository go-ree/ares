package util

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	DefaultPageNum  int64 = 1
	DefaultPageSize int64 = 5
	MaxPageSize     int64 = 200
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
	// PageInteger distinguishes omission (use the default) from explicit null.
	PageNum  PageInteger `form:"page_num" json:"page_num,omitempty" swaggertype:"integer" minimum:"1"`
	PageSize PageInteger `form:"page_size" json:"page_size,omitempty" swaggertype:"integer" minimum:"1" maximum:"200"` // 前端可选 15 30 50 100 200
	Sort     *SortOption `form:"sort" json:"sort,omitempty" collection_format:"csv"`
}

// PageInteger is an optional JSON integer with presence tracking. An omitted
// field receives a server default; explicit null and every non-integer JSON
// representation are rejected by UnmarshalJSON.
type PageInteger struct {
	value   int64
	present bool
}

func (value *PageInteger) UnmarshalJSON(payload []byte) error {
	payload = bytes.TrimSpace(payload)
	if bytes.Equal(payload, []byte("null")) {
		return errors.New("pagination value must be an integer, not null")
	}
	var parsed int64
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return errors.New("pagination value must be an integer")
	}
	value.value, value.present = parsed, true
	return nil
}

func (value PageInteger) MarshalJSON() ([]byte, error) {
	if !value.present {
		return []byte("null"), nil
	}
	return strconv.AppendInt(nil, value.value, 10), nil
}

func (value PageInteger) isSet() bool { return value.present }

func (value PageInteger) int64() int64 { return value.value }

func (value *PageInteger) set(parsed int64) {
	value.value, value.present = parsed, true
}

// SortOption 排序选项
type SortOption struct {
	Field     string `json:"field"`     // 排序字段
	Direction string `json:"direction"` // 排序方向：asc/desc
}

func NewUtilManager() *ParamPage {
	return &ParamPage{}
}

// GetPageNum 获取已解析的页码；未提供时返回 0，调用方应先执行
// NormalizePagination 以应用默认值并验证范围。
func (p *ParamPage) GetPageNum() int {
	if p == nil || !p.PageNum.isSet() {
		return 0
	}
	maxInt := int64(^uint(0) >> 1)
	if p.PageNum.int64() < 1 || p.PageNum.int64() > maxInt {
		return 0
	}
	return int(p.PageNum.int64())
}

// GetPageSize 获取已解析的每页大小。
func (p *ParamPage) GetPageSize() int {
	if p == nil || !p.PageSize.isSet() || p.PageSize.int64() < 1 || p.PageSize.int64() > MaxPageSize {
		return 0
	}
	return int(p.PageSize.int64())
}

// SetPageNum 设置页码
func (p *ParamPage) SetPageNum(pageNum int) {
	p.PageNum.set(int64(pageNum))
}

// SetPageSize 设置每页大小
func (p *ParamPage) SetPageSize(pageSize int) {
	p.PageSize.set(int64(pageSize))
}

// NormalizePagination applies defaults only to omitted values, enforces the
// public 200-row ceiling and proves the xorm int offset cannot overflow.
func (p *ParamPage) NormalizePagination(params *ParamPage) (int, int, error) {
	if params == nil {
		return 0, 0, errors.New("分页参数不能为空")
	}
	pageNum, pageSize := DefaultPageNum, DefaultPageSize
	if params.PageNum.isSet() {
		pageNum = params.PageNum.int64()
	}
	if params.PageSize.isSet() {
		pageSize = params.PageSize.int64()
	}
	if pageNum < 1 {
		return 0, 0, errors.New("page_num 必须是大于 0 的整数")
	}
	if pageSize < 1 || pageSize > MaxPageSize {
		return 0, 0, fmt.Errorf("page_size 必须是 1 到 %d 的整数", MaxPageSize)
	}
	maxInt := int64(^uint(0) >> 1)
	pageIndex := pageNum - 1
	if pageNum > maxInt || pageIndex > maxInt/pageSize {
		return 0, 0, errors.New("page_num 超出可支持范围")
	}
	offset := pageIndex * pageSize
	params.SetPageNum(int(pageNum))
	params.SetPageSize(int(pageSize))
	return int(pageSize), int(offset), nil
}

// CalculateTotalPages 计算总页数
func (p *ParamPage) CalculateTotalPages(total int64, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	size := int64(pageSize)
	pages := total / size
	if total%size != 0 {
		pages++
	}
	maxInt := int64(^uint(0) >> 1)
	if pages > maxInt {
		return int(maxInt)
	}
	return int(pages)
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
