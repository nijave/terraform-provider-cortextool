package cortextool

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/grafana/cortex-tools/pkg/rules"
	"github.com/grafana/cortex-tools/pkg/rules/rwrulefmt"
	"github.com/grafana/loki/pkg/ruler"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/prometheus/prometheus/pkg/rulefmt"

	"github.com/hashicorp/go-cty/cty"
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
				StateFunc:        NormalizeNamespaceYAML,
				ValidateDiagFunc: validateNamespaceYAML,
				DiffSuppressFunc: diffNamespaceYAML,
				Required:         true,
			},
		},
	}
}

func getRuleNamespaceFromYAML(configYaml string) (rules.RuleNamespace, error) {
	var ruleNamespace rules.RuleNamespace

	// See https://github.com/grafana/cortex-tools/blob/main/pkg/rules/parser.go#L107
	decoder := yaml.NewDecoder(strings.NewReader(configYaml))
	decoder.KnownFields(true)

	var ruleNamespaces []rules.RuleNamespace
	for {
		var ns rules.RuleNamespace
		err := decoder.Decode(&ns)
		if err == io.EOF {
			break
		}
		if err != nil {
			return ruleNamespace, err
		}

		// the upstream loki validator only validates the rulefmt rule groups,
		// not the remote write configs this type attaches.
		var groups []rulefmt.RuleGroup
		for _, g := range ns.Groups {
			groups = append(groups, g.RuleGroup)
		}

		if errs := ruler.ValidateGroups(groups...); len(errs) > 0 {
			messages := make([]string, len(errs))
			for i, err := range errs {
				messages[i] = err.Error()
			}
			return ruleNamespace, errors.New("The following errors were encountered validating rule groups:\n" + strings.Join(messages, "\n"))
		}

		ruleNamespaces = append(ruleNamespaces, ns)

	}

	if len(ruleNamespaces) > 1 {
		return ruleNamespace, fmt.Errorf("namespace definition contains more than one namespace which is not supported")
	}

	if len(ruleNamespaces) == 1 {
		return ruleNamespaces[0], nil
	}

	return ruleNamespace, fmt.Errorf("no namespace definition found")
}

func lintRuleNamespace(ruleNamespace rules.RuleNamespace) error {
	_, _, err := ruleNamespace.LintExpressions(rules.LokiBackend)
	return err
}

func getRuleNamespace(ctx context.Context, d *schema.ResourceData, meta any) (
	[]rwrulefmt.RuleGroup, error) {
	client := *meta.(*client).cli
	namespace := d.Get("namespace").(string)

	ruleGroups, err := client.ListRules(ctx, namespace)
	if err != nil {
		return nil, err
	}
	return ruleGroups[namespace], nil
}

func createRuleNamespace(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := *meta.(*client).cli
	namespace := d.Get("namespace").(string)
	configYaml := d.Get("config_yaml").(string)

	ruleNamespace, err := getRuleNamespaceFromYAML(configYaml)
	if err != nil {
		return diag.FromErr(err)
	}

	err = lintRuleNamespace(ruleNamespace)
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
	client := *meta.(*client).cli
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

	d.Set("config_yaml", NormalizeNamespaceYAML(string(configYamlBytes)))
	return diags
}

func updateRuleNamespace(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	var diags diag.Diagnostics
	client := *meta.(*client).cli
	namespace := d.Get("namespace").(string)
	configYaml := d.Get("config_yaml").(string)

	errDiag := createRuleNamespace(ctx, d, meta)
	if errDiag != nil {
		return errDiag
	}
	// Clean up the rules which need to be updated have been so with createRuleNamespace,
	// we still need to delete the rules which have been removed from the definition.
	ruleNamespace, err := getRuleNamespaceFromYAML(configYaml)
	if err != nil {
		return diag.FromErr(err)
	}
	err = lintRuleNamespace(ruleNamespace)
	if err != nil {
		return diag.FromErr(err)
	}

	var nsGroupNames []string
	for _, group := range ruleNamespace.Groups {
		nsGroupNames = append(nsGroupNames, group.Name)
	}

	// the ones which are configured in the rulers as per readRuleNamespace
	localNamespaces, err := getRuleNamespace(ctx, d, meta)
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
	client := *meta.(*client).cli
	namespace := d.Get("namespace").(string)

	ruleGroups, err := getRuleNamespace(ctx, d, meta)
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

// Borrowed from https://github.com/grafana/terraform-provider-grafana/blob/master/grafana/resource_dashboard.go
func NormalizeNamespaceYAML(config any) string {
	configYaml := config.(string)
	// control on namespace definition is done by validateNamespaceYAML and we have no way to return the error.
	ruleNamespace, _ := getRuleNamespaceFromYAML(configYaml)
	_, _, err := ruleNamespace.LintExpressions(rules.LokiBackend)
	if err != nil {
		panic(err)
	}
	// Loki might return the groups and/or rules in a different order than the one in the definition.
	// Let's sort them to make sure we can identify difference without false positive.
	sort.Slice(ruleNamespace.Groups, func(i, j int) bool {
		return ruleNamespace.Groups[i].Name < ruleNamespace.Groups[j].Name
	})
	for _, group := range ruleNamespace.Groups {
		sort.Slice(group.Rules, func(i, j int) bool {
			return group.Rules[i].Expr.Value < group.Rules[j].Expr.Value
		})
	}
	namespaceBytes, _ := yaml.Marshal(ruleNamespace.Groups)
	if namespaceBytes[len(namespaceBytes)-1] == '\n' {
		namespaceBytes = namespaceBytes[:len(namespaceBytes)-1]
	}
	if storeRulesSHA256 {
		configHash := sha256.Sum256(namespaceBytes)
		return fmt.Sprintf("%x", configHash[:])
	}
	return string(namespaceBytes)
}

func validateNamespaceYAML(config any, k cty.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	configYaml := config.(string)
	_, err := getRuleNamespaceFromYAML(configYaml)
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

func getRuleGroupsFromYaml(configYaml string) ([]rwrulefmt.RuleGroup, error) {
	var parsedGroups []rwrulefmt.RuleGroup

	//decoder := yaml.NewDecoder(strings.NewReader(configYaml))
	//decoder.KnownFields(true)
	//
	//for {
	//	var g rwrulefmt.RuleGroup
	//	err := decoder.Decode(&g)
	//	if err == io.EOF {
	//		break
	//	}
	//	if err != nil {
	//		return parsedGroups, err
	//	}
	//
	//	parsedGroups = append(parsedGroups, g)
	//}

	err := yaml.Unmarshal([]byte(configYaml), &parsedGroups)

	return parsedGroups, err
}

func diffNamespaceYAML(k, oldValue, newValue string, d *schema.ResourceData) bool {
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
