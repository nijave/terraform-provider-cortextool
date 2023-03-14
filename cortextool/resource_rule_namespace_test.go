package cortextool

import (
	"context"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"os"
	"strconv"
	"sync"
	"testing"
)

const expectedInitialConfig = `namespace: grafana-agent-traces
groups:
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

const expectedInitialConfigAfterUpdate = `namespace: grafana-agent-traces
groups:
    - name: grafana-agent
      rules:
        - alert: LogWarnMessages
          expr: |-
            (sum(rate({deployment="grafana-agent-traces"} |= "level=warn"[1m])) > 0.1)
          for: 5m
          labels:
            team: sre
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

	expectedInitial := map[bool]string{
		false: expectedInitialConfig,
		true:  "c429c8535b84806d61c9188e6df30f985067b2f74f1daac0e8f5350e6551e53f",
	}

	expectedUpdate := map[bool]string{
		false: expectedInitialConfigAfterUpdate,
		true:  "7d9dd2c1b2178501420cd5dbfa2d16535ed13544f41a966a1b53d461dc828d06",
	}

	envVar := "CORTEXTOOL_STORE_RULES_SHA256"
	for _, storeAsHash := range []bool{false, true} {
		os.Setenv(envVar, strconv.FormatBool(storeAsHash))

		resource.UnitTest(t, resource.TestCase{
			PreCheck:          func() { testAccPreCheck(t) },
			ProviderFactories: testAccProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: `
						resource "cortextool_rule_namespace" "demo" {
							namespace = "grafana-agent-traces"
							config_yaml = file("testdata/rules.yaml")
						  }
						`,
					Check: resource.ComposeTestCheckFunc(
						resource.TestCheckResourceAttr(
							"cortextool_rule_namespace.demo", "namespace", "grafana-agent-traces"),
						resource.TestCheckResourceAttr(
							"cortextool_rule_namespace.demo", "config_yaml", expectedInitial[storeAsHash]),
					),
				},
				{
					Config: `
						resource "cortextool_rule_namespace" "demo" {
							namespace = "grafana-agent-traces"
							config_yaml = file("testdata/rules2.yaml")
						 }
						`,
					Check: resource.ComposeTestCheckFunc(
						resource.TestCheckResourceAttr(
							"cortextool_rule_namespace.demo", "namespace", "grafana-agent-traces"),
						resource.TestCheckResourceAttr(
							"cortextool_rule_namespace.demo", "config_yaml", expectedUpdate[storeAsHash]),
					),
				},
			},
		})

		os.Unsetenv(envVar)
	}
}

// Note: Not all whitespace changes will yield the same output from the linter.
// Changing string format from a single yaml line to multiline with |
// will result in different linted yaml even though the rules are logically the same
func TestAccResourceNamespaceWhitespaceChanges(t *testing.T) {
	os.Setenv("CORTEXTOOL_ADDRESS", "http://localhost:8080")
	defer os.Unsetenv("CORTEXTOOL_ADDRESS")

	expected := map[bool]string{
		false: expectedInitialConfigAfterUpdate,
		true:  "7d9dd2c1b2178501420cd5dbfa2d16535ed13544f41a966a1b53d461dc828d06",
	}

	envVar := "CORTEXTOOL_STORE_RULES_SHA256"
	for _, storeAsHash := range []bool{false, true} {
		os.Setenv(envVar, strconv.FormatBool(storeAsHash))

		resource.UnitTest(t, resource.TestCase{
			PreCheck:          func() { testAccPreCheck(t) },
			ProviderFactories: testAccProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: `
						resource "cortextool_rule_namespace" "demo" {
							namespace = "grafana-agent-traces"
							config_yaml = file("testdata/rules2.yaml")
						  }
						`,
					Check: resource.ComposeTestCheckFunc(
						resource.TestCheckResourceAttr(
							"cortextool_rule_namespace.demo", "namespace", "grafana-agent-traces"),
						resource.TestCheckResourceAttr(
							"cortextool_rule_namespace.demo", "config_yaml", expected[storeAsHash]),
					),
				},
				{
					Config: `
						resource "cortextool_rule_namespace" "demo" {
							namespace = "grafana-agent-traces"
							config_yaml = file("testdata/rules2_whitespace.yaml")
						}
						`,
					Check: resource.ComposeTestCheckFunc(
						resource.TestCheckResourceAttr(
							"cortextool_rule_namespace.demo", "namespace", "grafana-agent-traces"),
						resource.TestCheckResourceAttr(
							"cortextool_rule_namespace.demo", "config_yaml", expected[storeAsHash]),
					),
				},
			},
		})

		os.Unsetenv(envVar)
	}
}
