package app

import (
	"context"
	"strings"
)

type CodePackageTypeOptions struct {
	Default string   `json:"default"`
	Allowed []string `json:"allowed"`
}

type AppConfigOptions struct {
	DevLanguage     string                 `json:"dev_language"`
	CodePackageType CodePackageTypeOptions `json:"code_package_type"`
}

func (am *AppManager) GetAppConfigOptions(ctx context.Context, appID int64) (*AppConfigOptions, error) {
	validator := NewAppValidator()
	if err := validator.ValidateAppID(appID); err != nil {
		return nil, NewValidationError(err.Error())
	}
	appRow, err := am.GetAppByID(ctx, appID)
	if err != nil {
		return nil, err
	}
	rules, err := loadDevLanguageRules(ctx, appRow.DevLanguage)
	if err != nil {
		return nil, NewValidationError(err.Error())
	}
	return &AppConfigOptions{
		DevLanguage: strings.ToLower(strings.TrimSpace(appRow.DevLanguage)),
		CodePackageType: CodePackageTypeOptions{
			Default: rules.Default,
			Allowed: rules.Allowed,
		},
	}, nil
}
