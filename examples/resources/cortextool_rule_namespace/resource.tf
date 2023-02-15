resource "cortextool_rule_namespace" "demo" {
  namespace = "demo"
  # See cortextool/testsdata/rules.yaml
  config_yaml = file("testdata/rules.yaml")
}
