package app

import (
	"strings"
	"testing"
)

func TestValidateCreateAppRejectsLegacyNullRequiredText(t *testing.T) {
	request := &CreateAppRequest{
		AppName:       "demo-app",
		AppNameCN:     " NULL ",
		Owner:         "demo.user",
		OwnerCN:       "演示用户",
		DevLanguage:   "golang",
		DescriptionCN: "演示应用",
		GitUrl:        "git@example.com:demo/demo-app.git",
	}

	err := NewAppValidator().ValidateCreateApp(request)
	if err == nil || !strings.Contains(err.Error(), "app_name_cn 不能为空") {
		t.Fatalf("ValidateCreateApp() error = %v, want app_name_cn validation error", err)
	}

	request.AppNameCN = "演示应用"
	request.DescriptionCN = "null"
	err = NewAppValidator().ValidateCreateApp(request)
	if err == nil || !strings.Contains(err.Error(), "description_cn 不能为空") {
		t.Fatalf("ValidateCreateApp() error = %v, want description_cn validation error", err)
	}
}

func TestBuildPatchAppMapWritesNullableTextAsSQLNull(t *testing.T) {
	description := " Null "
	rundeckAppName := " nUlL "
	request := PatchAppRequest{DescriptionCN: &description, RundeckAppName: &rundeckAppName}
	if err := NewAppValidator().ValidatePatchApp(&request); err != nil {
		t.Fatalf("ValidatePatchApp() rejected a nullable sentinel: %v", err)
	}
	updates, err := buildPatchAppMap(request)
	if err != nil {
		t.Fatalf("buildPatchAppMap() error = %v", err)
	}
	if value, exists := updates["description_cn"]; !exists || value != nil {
		t.Fatalf("description_cn update = %#v, want explicit nil", value)
	}
	if value, exists := updates["rundeck_app_name"]; !exists || value != nil {
		t.Fatalf("rundeck_app_name update = %#v, want explicit nil", value)
	}

	appNameCN := "NULL"
	if _, err := buildPatchAppMap(PatchAppRequest{AppNameCN: &appNameCN}); err == nil {
		t.Fatal("buildPatchAppMap() accepted a null sentinel for required app_name_cn")
	}
}

func TestBuildUpdateMapNormalizesNullableText(t *testing.T) {
	nullSentinel := " nUlL "
	normalValue := " ./build "
	updates, err := buildUpdateMap(UpdateAppConfigRequest{
		CodePackagePath: &normalValue,
		CodePackageName: &nullSentinel,
		BaseImage:       &nullSentinel,
		PreStopCommand:  &nullSentinel,
	})
	if err != nil {
		t.Fatalf("buildUpdateMap() error = %v", err)
	}
	if updates["code_package_path"] != "./build" {
		t.Fatalf("code_package_path = %#v, want normalized value", updates["code_package_path"])
	}
	for _, field := range []string{"code_package_name", "base_image", "pre_stop_command"} {
		if value, exists := updates[field]; !exists || value != nil {
			t.Fatalf("%s update = %#v, want explicit nil", field, value)
		}
	}

	if _, err := buildUpdateMap(UpdateAppConfigRequest{CodePackageType: &nullSentinel}); err == nil {
		t.Fatal("buildUpdateMap() accepted a null sentinel for required code_package_type")
	}
}

func TestUnknownLanguageHasNoSentinelPackageType(t *testing.T) {
	if got := getDefaultPackageType("unknown"); got != "" {
		t.Fatalf("getDefaultPackageType() = %q, want empty value", got)
	}
}
