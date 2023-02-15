package cortextool

import (
	"context"
	"github.com/grafana/cortex-tools/pkg/rules"
	"github.com/grafana/cortex-tools/pkg/rules/rwrulefmt"
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
				StateFunc:        formatRuleNamespace,
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

func getRuleGroupsFromYaml(configYaml string) ([]rwrulefmt.RuleGroup, error) {
	namespace, err := getRuleNamespaceFromYaml(configYaml)
	if err != nil {
		return nil, err
	}
	return namespace.Groups, nil
}

func formatRuleNamespace(any interface{}) string {
	configYaml := any.(string)
	namespace, err := getRuleNamespaceFromYaml(configYaml)
	if err != nil {
		return ""
	}
	newYamlBytes, _ := yaml.Marshal(&namespace)
	return string(newYamlBytes)
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
	oldGroup, err := getRuleGroupsFromYaml(oldValue)
	if err != nil {
		tflog.Warn(context.Background(), "Failed to unmarshal oldGroup value")
		tflog.Debug(context.Background(), err.Error())
		return false
	}

	newGroup, err := getRuleGroupsFromYaml(newValue)
	if err != nil {
		tflog.Warn(context.Background(), "Failed to unmarshal newGroup value")
		tflog.Debug(context.Background(), err.Error())
		return false
	}

	return rules.CompareNamespaces(rules.RuleNamespace{
		Namespace: "",
		Filepath:  "",
		Groups:    oldGroup,
	}, rules.RuleNamespace{
		Namespace: "",
		Filepath:  "",
		Groups:    newGroup,
	}).State == rules.Unchanged
}

func getRuleNamespaceRemote(ctx context.Context, d *schema.ResourceData, meta any) (
	[]rwrulefmt.RuleGroup, error) {
	client := *meta.(*providerData).cli
	namespace := d.Get("namespace").(string)

	ruleGroups, err := client.ListRules(ctx, namespace)
	if err != nil {
		return nil, err
	}
	return ruleGroups[namespace], nil
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
	client := *meta.(*providerData).cli
	namespace := d.Get("namespace").(string)

	remoteNamespaceRuleGroup, err := client.ListRules(ctx, namespace)
	if err != nil {
		if err.Error() == "requested resource not found" {
			d.SetId("")
			return diags
		} else {
			return diag.FromErr(err)
		}
	}

	// Loki top level key is the namespace name while in the YAML definition the top level key is groups
	// Let's rename the key to be able to have a nice difference
	remoteNamespaceRuleGroup["groups"] = remoteNamespaceRuleGroup[namespace]
	delete(remoteNamespaceRuleGroup, namespace)

	configYamlBytes, err := yaml.Marshal(remoteNamespaceRuleGroup)
	if err != nil {
		return diag.FromErr(err)
	}

	d.Set("config_yaml", string(configYamlBytes))
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
	currentGroupsNames := make([]string, 0, len(localNamespaces))
	for _, group := range localNamespaces {
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

	ruleGroups, err := getRuleNamespaceRemote(ctx, d, meta)
	if err != nil {
		return diag.FromErr(err)
	}

	for _, groupName := range ruleGroups {
		err :=
			client.DeleteRuleGroup(ctx, namespace, groupName.Name)
		if err != nil {
			return diag.FromErr(err)
		}
	}

	d.SetId("")
	return diags
}
