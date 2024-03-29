---
# generated by https://github.com/hashicorp/terraform-plugin-docs
page_title: "cortextool_rule_namespace Resource - terraform-provider-cortextool"
subcategory: ""
description: |-
  Official documentation https://grafana.com/docs/loki/latest/rules/HTTP API https://grafana.com/docs/loki/latest/api/#ruler
---

# cortextool_rule_namespace (Resource)

* [Official documentation](https://grafana.com/docs/loki/latest/rules/)
* [HTTP API](https://grafana.com/docs/loki/latest/api/#ruler)

## Example Usage

```terraform
resource "cortextool_rule_namespace" "demo" {
  namespace = "demo"
  # See cortextool/testsdata/rules.yaml
  config_yaml = file("testdata/rules.yaml")
}
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `config_yaml` (String) The namespace's groups rules definition to create
- `namespace` (String) The name of the namespace to create in Grafana

### Read-Only

- `id` (String) The ID of this resource.
