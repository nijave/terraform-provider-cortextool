package cortextool

import (
	"context"
	"github.com/grafana/cortex-tools/pkg/rules/rwrulefmt"
)

type providerData struct {
	cli *CortexRuleClient
}

type CortexRuleClient interface {
	CreateRuleGroup(context.Context, string, rwrulefmt.RuleGroup) error
	DeleteRuleGroup(context.Context, string, string) error
	ListRules(context.Context, string) (map[string][]rwrulefmt.RuleGroup, error)
}
