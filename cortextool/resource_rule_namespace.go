package cortextool

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/grafana/cortex-tools/pkg/rules"
	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
)

func resourceRuleNamespace() *schema.Resource {
	return &schema.Resource{
		Description: `
* [Official documentation](https://grafana.com/docs/loki/latest/rules/)
* [HTTP API](https://grafana.com/docs/loki/latest/api/#ruler)
`,

		CreateContext: createRuleNamespace,
		ReadContext:   readRuleNamespace,
		UpdateContext: updateRuleNamespace,
		DeleteContext: deleteRuleNamespace,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"namespace": {
				Description: "The name of the namespace to create in Grafana",
				Type:        schema.TypeString,
				Required:    true,
			},
			"config_yaml": {
				Description:      "The namespace's groups rules definition to create",
				Type:             schema.TypeString,
				StateFunc:        stateFunction,
				ValidateDiagFunc: validateNamespaceYaml,
				DiffSuppressFunc: diffNamespaceRules,
				Required:         true,
			},
		},
	}
}

func getRuleNamespaceFromYaml(configYaml string) (rules.RuleNamespace, error) {
	var namespace rules.RuleNamespace
	err := yaml.Unmarshal([]byte(configYaml), &namespace)
	if err != nil {
		return namespace, err
	}
	namespace.LintExpressions(rules.LokiBackend)
	return namespace, nil
}

func formatRuleNamespace(ruleNamespace rules.RuleNamespace) string {
	newYamlBytes, _ := yaml.Marshal(&ruleNamespace)

	if storeRulesSha256 {
		configHash := sha256.Sum256(newYamlBytes)
		return fmt.Sprintf("%x", configHash[:])
	}

	return string(newYamlBytes)
}

func stateFunction(meta any) string {
	configYaml := meta.(string)
	namespace, _ := getRuleNamespaceFromYaml(configYaml)
	return formatRuleNamespace(namespace)
}

func validateNamespaceYaml(config any, k cty.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	configYaml := config.(string)
	_, err := getRuleNamespaceFromYaml(configYaml)
	if err != nil {
		return diag.Diagnostics{
			diag.Diagnostic{
				Severity:      diag.Error,
				Summary:       "Namespace definition is not valid.",
				Detail:        err.Error(),
				AttributePath: k,
			},
		}
	}
	return diags
}

func diffNamespaceRules(k, oldValue, newValue string, d *schema.ResourceData) bool {
	// If we cannot unmarshal, as we cannot return an error, let's say there is a difference
	oldNamespace, err := getRuleNamespaceFromYaml(oldValue)
	if err != nil {
		tflog.Warn(context.Background(), "Failed to unmarshal oldGroup value")
		tflog.Debug(context.Background(), err.Error())
		return false
	}

	newNamespace, err := getRuleNamespaceFromYaml(newValue)
	if err != nil {
		tflog.Warn(context.Background(), "Failed to unmarshal newGroup value")
		tflog.Debug(context.Background(), err.Error())
		return false
	}

	return rules.CompareNamespaces(oldNamespace, newNamespace).State == rules.Unchanged
}

func getRuleNamespaceRemote(ctx context.Context, d *schema.ResourceData, meta any) (
	rules.RuleNamespace, error) {
	client := *meta.(*providerData).cli
	namespace := d.Get("namespace").(string)

	ruleGroups, err := client.ListRules(ctx, namespace)
	if err != nil {
		return rules.RuleNamespace{}, err
	}
	return rules.RuleNamespace{
		Namespace: namespace,
		Filepath:  "",
		Groups:    ruleGroups[namespace],
	}, nil
}

func createRuleNamespace(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := *meta.(*providerData).cli
	namespace := d.Get("namespace").(string)
	configYaml := d.Get("config_yaml").(string)

	ruleNamespace, err := getRuleNamespaceFromYaml(configYaml)
	if err != nil {
		return diag.FromErr(err)
	}

	for _, group := range ruleNamespace.Groups {
		err = client.CreateRuleGroup(ctx, namespace, group)
		if err != nil {
			return diag.FromErr(err)
		}
	}

	d.SetId(hash(namespace))
	return readRuleNamespace(ctx, d, meta)
}

func readRuleNamespace(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	var diags diag.Diagnostics

	ruleNamespace, err := getRuleNamespaceRemote(ctx, d, meta)
	if err != nil {
		return diag.FromErr(err)
	}
	configString := formatRuleNamespace(ruleNamespace)

	d.Set("config_yaml", configString)
	return diags
}

func updateRuleNamespace(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	var diags diag.Diagnostics
	client := *meta.(*providerData).cli
	namespace := d.Get("namespace").(string)
	configYaml := d.Get("config_yaml").(string)

	errDiag := createRuleNamespace(ctx, d, meta)
	if errDiag != nil {
		return errDiag
	}
	// Clean up the rules which need to be updated have been so with createRuleNamespace,
	// we still need to delete the rules which have been removed from the definition.
	ruleNamespace, err := getRuleNamespaceFromYaml(configYaml)
	if err != nil {
		return diag.FromErr(err)
	}

	var nsGroupNames []string
	for _, group := range ruleNamespace.Groups {
		nsGroupNames = append(nsGroupNames, group.Name)
	}

	// the ones which are configured in the rulers as per readRuleNamespace
	localNamespaces, err := getRuleNamespaceRemote(ctx, d, meta)
	if err != nil {
		return diag.FromErr(err)
	}
	currentGroupsNames := make([]string, 0, len(localNamespaces.Groups))
	for _, group := range localNamespaces.Groups {
		currentGroupsNames = append(currentGroupsNames, group.Name)
	}

	// All groups present in Loki but not in the YAML definition must be deleted
	for _, name := range currentGroupsNames {
		if !slices.Contains(nsGroupNames, name) {
			errRaw := client.DeleteRuleGroup(ctx, namespace, name)
			if err != nil {
				return diag.FromErr(errRaw)
			}
		}
	}
	return diags
}

func deleteRuleNamespace(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	var diags diag.Diagnostics
	client := *meta.(*providerData).cli
	namespace := d.Get("namespace").(string)

	ruleNamespace, err := getRuleNamespaceRemote(ctx, d, meta)
	if err != nil {
		return diag.FromErr(err)
	}

	for _, groupName := range ruleNamespace.Groups {
		err :=
			client.DeleteRuleGroup(ctx, namespace, groupName.Name)
		if err != nil {
			return diag.FromErr(err)
		}
	}

	d.SetId("")
	return diags
}
