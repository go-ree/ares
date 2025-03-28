package util

import (
	"strings"
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
