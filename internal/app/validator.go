package app

import (
	"ares/internal/tool"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// AppValidator 应用验证器
type AppValidator struct {
}

// NewAppValidator 创建应用验证器实例
func NewAppValidator() *AppValidator {
	return &AppValidator{}
}

// ValidateAppID 验证应用ID是否在有效范围内
func (v *AppValidator) ValidateAppID(appID int64) error {
	if appID < 10000 || appID > 99999 {
		return errors.New("应用ID必须在10000-99999范围内")
	}
	return nil
}

// ValidateCreateApp 验证创建应用请求
func (v *AppValidator) ValidateCreateApp(req *CreateAppRequest) error {
	err := tool.ValidateStruct(req)
	if err != nil {
		return err
	}

	// 应用名称校验
	namePattern := regexp.MustCompile(`^[a-z][a-z0-9\-]{2,24}$`)
	if !namePattern.MatchString(req.AppName) {
		return errors.New("应用名称必须以小写字母开头，只能包含小写字母、数字和连字符，长度3-25")
	}

	// Git URL格式验证
	if !isValidGitURL(req.GitUrl) {
		return errors.New("git地址必须使用SSH协议，且以.git结尾。示例：git@gitlab.ttpai.work:group/repo.git")
	}

	// 负责人 格式校验
	ownerPattern := regexp.MustCompile(`^[a-z]+\.[a-z]+$`)
	if !ownerPattern.MatchString(req.Owner) {
		return errors.New("负责人格式不正确，必须为 'san.zhang' 或 'si.li' 形式")
	}

	// 开发语言校验
	validLanguages := map[string]bool{
		"java":    true,
		"golang":  true,
		"python":  true,
		"node.js": true,
	}

	if _, ok := validLanguages[strings.ToLower(req.DevLanguage)]; !ok {
		validLangs := getMapKeys(validLanguages)
		return fmt.Errorf("不支持的开发语言: %s，支持的语言包括: %s。 ps.如需新增开发语言，请联系基础运维同学", req.DevLanguage, strings.Join(validLangs, ", "))
	}

	return nil
}

// ValidatePatchApp 验证应用基本信息变更（只校验传入字段）
func (v *AppValidator) ValidatePatchApp(req *PatchAppRequest) error {
	if req == nil {
		return errors.New("请求不能为空")
	}
	// 必须至少更新一个字段
	if req.AppNameCN == nil &&
		req.Owner == nil &&
		req.OwnerCN == nil &&
		req.DevLanguage == nil &&
		req.DescriptionCN == nil &&
		req.GitUrl == nil &&
		req.RundeckAppName == nil {
		return errors.New("没有需要更新的字段")
	}

	namePattern := regexp.MustCompile(`^[a-z][a-z0-9\-]{2,24}$`)
	ownerPattern := regexp.MustCompile(`^[a-z]+\.[a-z]+$`)

	if req.RundeckAppName != nil {
		s := strings.TrimSpace(*req.RundeckAppName)
		if s != "" && !namePattern.MatchString(s) {
			return errors.New("rundeck_app_name 格式不正确：必须以小写字母开头，只能包含小写字母、数字和连字符，长度3-25")
		}
	}
	if req.Owner != nil {
		s := strings.TrimSpace(*req.Owner)
		if s == "" || !ownerPattern.MatchString(s) {
			return errors.New("负责人格式不正确，必须为 'san.zhang' 或 'si.li' 形式")
		}
	}
	if req.GitUrl != nil {
		s := strings.TrimSpace(*req.GitUrl)
		if s == "" || !isValidGitURL(s) {
			return errors.New("git地址必须使用SSH协议，且以.git结尾。示例：git@gitlab.ttpai.work:group/repo.git")
		}
	}
	if req.DevLanguage != nil {
		s := strings.TrimSpace(*req.DevLanguage)
		if s == "" {
			return errors.New("dev_language 不能为空")
		}
		validLanguages := map[string]bool{
			"java":    true,
			"golang":  true,
			"python":  true,
			"node.js": true,
		}
		if _, ok := validLanguages[strings.ToLower(s)]; !ok {
			validLangs := getMapKeys(validLanguages)
			return fmt.Errorf("不支持的开发语言: %s，支持的语言包括: %s。 ps.如需新增开发语言，请联系基础运维同学", s, strings.Join(validLangs, ", "))
		}
	}
	// 其它字段仅做“传了但全空白”阻断（更细的长度限制按需再加）
	if req.AppNameCN != nil && strings.TrimSpace(*req.AppNameCN) == "" {
		return errors.New("app_name_cn 不能为空")
	}
	if req.OwnerCN != nil && strings.TrimSpace(*req.OwnerCN) == "" {
		return errors.New("owner_cn 不能为空")
	}
	return nil
}

// 辅助函数：获取map的所有key
func getMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// isValidGitURL 验证Git URL的格式
func isValidGitURL(url string) bool {
	// 只支持SSH协议的Git仓库地址(git@)
	if !strings.HasPrefix(url, "git@") {
		return false
	}

	// 进一步验证格式: git@gitlab.ttpai.work:group/repo.git
	pattern := regexp.MustCompile(`^git@[\w\.-]+:[\w\.-]+(?:/[\w\.-]+)*\.git$`)
	return pattern.MatchString(url)
}
