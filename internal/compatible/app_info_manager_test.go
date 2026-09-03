package compatible

import (
	"testing"

	"github.com/go-ree/ares/internal/environment"
)

func TestResolveCompatibleEnvironmentUsesDynamicCatalog(t *testing.T) {
	catalog := []environment.View{
		{Code: "qa-cn", Name: "质量环境", Enabled: true, SortOrder: 10},
		{Code: "prod-blue", Name: "蓝色生产", Enabled: true, SortOrder: 20},
	}
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "default follows first catalog item", value: "", want: "qa-cn"},
		{name: "accepts normalized code", value: " QA-CN ", want: "qa-cn"},
		{name: "accepts display name", value: "蓝色生产", want: "prod-blue"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveCompatibleEnvironment(test.value, catalog)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("resolveCompatibleEnvironment(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestResolveCompatibleEnvironmentRejectsUnknownOrEmptyCatalog(t *testing.T) {
	if _, err := resolveCompatibleEnvironment("missing", []environment.View{{Code: "qa", Name: "QA", Enabled: true}}); err == nil {
		t.Fatal("unknown environment should fail")
	}
	if _, err := resolveCompatibleEnvironment("", nil); err == nil {
		t.Fatal("empty enabled catalog should fail")
	}
}
