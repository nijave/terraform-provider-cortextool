package cortextool

import (
	"context"
	"errors"
	"github.com/grafana/cortex-tools/pkg/rules"
	"github.com/grafana/cortex-tools/pkg/rules/rwrulefmt"
)

type MockCortexRuleClient struct {
	namespaces map[string]rules.RuleNamespace
}

func NewMockCortexRuleClient() MockCortexRuleClient {
	return MockCortexRuleClient{
		namespaces: map[string]rules.RuleNamespace{},
	}
}

func (m MockCortexRuleClient) CreateRuleGroup(ctx context.Context, namespace string, group rwrulefmt.RuleGroup) error {
	m.DeleteRuleGroup(ctx, namespace, group.Name)
	if ns, ok := m.namespaces[namespace]; ok {
		ns.Groups = append(ns.Groups, group)
		m.namespaces[namespace] = ns
	} else {
		m.namespaces[namespace] = rules.RuleNamespace{Groups: []rwrulefmt.RuleGroup{group}}
	}
	m.namespaces[namespace].LintExpressions(rules.LokiBackend)
	return nil
}

func (m MockCortexRuleClient) DeleteRuleGroup(_ context.Context, namespace string, groupName string) error {
	foundGroup := false
	if ns, nsOk := m.namespaces[namespace]; nsOk {
		newGroups := make([]rwrulefmt.RuleGroup, 0, len(ns.Groups))
		for _, group := range ns.Groups {
			if group.Name == groupName {
				foundGroup = true
			} else {
				newGroups = append(newGroups, group)
			}
		}
		ns.Groups = newGroups
		m.namespaces[namespace] = ns

		if foundGroup {
			return nil
		}
	}

	return errors.New("requested resource not found")
}

func (m MockCortexRuleClient) ListRules(_ context.Context, namespace string) (map[string][]rwrulefmt.RuleGroup, error) {
	ns, ok := m.namespaces[namespace]
	if ok {
		return map[string][]rwrulefmt.RuleGroup{namespace: ns.Groups}, nil
	}

	return nil, errors.New("requested resource not found")
}
