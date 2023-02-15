package cortextool

import (
	"context"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"os"
	"sync"
	"testing"
)

const expectedInitialConfig = `groups:
    - name: grafana-agent
      rules:
        - alert: LogErrorMessages
          expr: '(sum(rate({deployment="grafana-agent-traces"} |= "level=error"[1m])) > 0.1)'
          for: 3m
          labels:
            route: team=sre
            team: sre
        - alert: LogWarnMessages
          expr: '(sum(rate({deployment="grafana-agent-traces"} |= "level=warn"[1m])) > 0.1)'
          for: 3m
          labels:
            route: team=sre
            team: sre
        - alert: LogInfoMessages
          expr: '(sum(rate({deployment="grafana-agent-traces"} |= "level=info"[1m])) > 0.1)'
          for: 3m
          labels:
            route: team=sre
            team: sre
`

const expectedInitialConfigAfterUpdate = `groups:
    - name: grafana-agent
      rules:
        - alert: LogWarnMessages
          expr: |-
            (sum(rate({deployment="grafana-agent-traces"} |= "level=warn"[1m])) > 0.1)
          for: 5m
          labels:
            team: sre
`

const testAccResourceNamespace = `
resource "cortextool_rule_namespace" "demo" {
	namespace = "grafana-agent-traces"
	config_yaml = file("testdata/rules.yaml")
  }
`

const testAccResourceNamespaceAfterUpdate = `
resource "cortextool_rule_namespace" "demo" {
	namespace = "grafana-agent-traces"
	config_yaml = file("testdata/rules2.yaml")
 }
`

var testAccProviderFactories map[string]func() (*schema.Provider, error)
var testAccProvider *schema.Provider
var testAccProviders map[string]*schema.Provider
var testAccProviderConfigure sync.Once

func init() {
	var cc CortexRuleClient
	cc = NewMockCortexRuleClient()

	testAccProvider = New("dev", &cc)()
	testAccProviders = map[string]*schema.Provider{
		"cortextool": New("dev", &cc)(),
	}

	// Always allocate a new provider instance each invocation, otherwise gRPC
	// ProviderConfigure() can overwrite configuration during concurrent testing.
	testAccProviderFactories = map[string]func() (*schema.Provider, error){
		"cortextool": func() (*schema.Provider, error) {
			return New("dev", &cc)(), nil
		},
	}
}

// testAccPreCheck verifies required provider testing configuration. It should
// be present in every acceptance test.
//
// These verifications and configuration are preferred at this level to prevent
// provider developers from experiencing less clear errors for every test.
func testAccPreCheck(t *testing.T) {
	testAccProviderConfigure.Do(func() {
		// Since we are outside the scope of the Terraform configuration we must
		// call Configure() to properly initialize the provider configuration.
		err := testAccProvider.Configure(context.Background(), terraform.NewResourceConfigRaw(nil))
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestAccResourceNamespace(t *testing.T) {
	os.Setenv("CORTEXTOOL_ADDRESS", "http://localhost:8080")
	defer os.Unsetenv("CORTEXTOOL_ADDRESS")

	resource.UnitTest(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceNamespace,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(
						"cortextool_rule_namespace.demo", "namespace", "grafana-agent-traces"),
					resource.TestCheckResourceAttr(
						"cortextool_rule_namespace.demo", "config_yaml", expectedInitialConfig),
				),
			},
			{
				Config: testAccResourceNamespaceAfterUpdate,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(
						"cortextool_rule_namespace.demo", "namespace", "grafana-agent-traces"),
					resource.TestCheckResourceAttr(
						"cortextool_rule_namespace.demo", "config_yaml", expectedInitialConfigAfterUpdate),
				),
			},
		},
	})

}
