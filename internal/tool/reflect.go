package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
)

// ToMapStringString 将任意类型的结构体转换为 map[string]string
// 传入：任意类型结构体
// 传出：map[string]string 、error
func ToMapStringString(v interface{}) (map[string]string, error) {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected a struct, got %s", val.Kind())
	}

	result := make(map[string]string)
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldName := typ.Field(i).Name

		// 忽略非导出字段
		if !field.CanInterface() {
			continue
		}

		// 将字段值转换为字符串
		var strValue string
		switch field.Kind() {
		case reflect.String:
			strValue = field.String()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			strValue = strconv.FormatInt(field.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			strValue = strconv.FormatUint(field.Uint(), 10)
		case reflect.Float32, reflect.Float64:
			strValue = strconv.FormatFloat(field.Float(), 'f', -1, 64)
		case reflect.Bool:
			strValue = strconv.FormatBool(field.Bool())
		default:
			strValue = fmt.Sprintf("%v", field.Interface())
		}

		result[fieldName] = strValue
	}

	return result, nil
}

// ToMapStringInterface 将任意类型转换为map[string]string类型
func ToMapStringInterface(v interface{}) (map[string]string, error) {
	var result map[string]string
	// 将 v 转换为 []byte
	jsonBytes, err := json.Marshal(v)
	if err != nil {
		return nil, err // 返回错误
	}
	err = json.Unmarshal(jsonBytes, &result)
	if err != nil {
		return nil, err // 返回错误
	}
	return result, nil // 返回转换后的 map
}

// ToJSON 将任意类型结构体转换为JSON字符串
func ToJSON(v interface{}) (string, error) {
	jsonBytes, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("JSON转换失败: %s", err)
	}

	// 美化JSON输出
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, jsonBytes, "", "    "); err != nil {
		return "", fmt.Errorf("JSON格式化失败: %s", err)
	}

	return prettyJSON.String(), nil
}
