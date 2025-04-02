package tool

import (
	"fmt"
	"reflect"
	"strings"
)

// ValidateStruct 通用的结构体验证函数
// 验证参数完整且字段不能为空
func ValidateStruct(s interface{}) error {
	if s == nil {
		return fmt.Errorf("请求不能为空")
	}

	v := reflect.ValueOf(s)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	t := v.Type()
	var emptyFields []string
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if field.Kind() == reflect.String && field.String() == "" {
			// 获取json标签
			jsonTag := t.Field(i).Tag.Get("json")
			// 处理json标签，去除可能存在的选项（如 omitempty）
			jsonField := strings.Split(jsonTag, ",")[0]
			if jsonField != "" {
				emptyFields = append(emptyFields, jsonField)
			}
		}
	}

	if len(emptyFields) > 0 {
		return fmt.Errorf("以下字段不能为空: %s", strings.Join(emptyFields, ", "))
	}

	return nil
}
