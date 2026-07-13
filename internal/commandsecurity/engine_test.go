package commandsecurity

import (
	"strings"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func testPolicy(operation pluginapi.OperationName) pluginapi.CustomCommandPolicy {
	class := pluginapi.ClassQuery
	rules := []pluginapi.CustomCommandRule{
		{ID: "allow-show", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.show ", Effect: pluginapi.CommandAllowLow},
		{ID: "block-show-secret", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.show blocked", Effect: pluginapi.CommandBlocked},
	}
	if operation == pluginapi.OperationCommandExecuteConfig {
		class = pluginapi.ClassConfig
		rules = []pluginapi.CustomCommandRule{
			{ID: "allow-set", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.set ", Effect: pluginapi.CommandAllowMedium},
			{ID: "high-set", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.set high-risk ", Effect: pluginapi.CommandAllowHigh},
			{ID: "block-set", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.set blocked", Effect: pluginapi.CommandBlocked},
		}
	}
	return pluginapi.CustomCommandPolicy{
		Operation: operation, Class: class,
		Limits: pluginapi.CustomCommandLimits{MaxCommands: 3, MaxCommandBytes: 32, MaxTotalBytes: 64, MaxCommandTimeout: time.Second, MaxTotalTimeout: 3 * time.Second, MaxOutputBytes: 1024},
		Rules: rules,
		OutputRedactions: []pluginapi.OutputRedaction{{Literal: "secret-token", Replacement: "[REDACTED]"}},
	}
}

func TestBlockedRuleAlwaysWins(t *testing.T) {
	engine := New()
	_, err := engine.Evaluate(testPolicy(pluginapi.OperationCommandExecuteConfig), pluginapi.OperationCommandExecuteConfig, map[string]any{"commands": []string{"fake.set blocked value"}})
	if !apperror.IsCode(err, apperror.CodeDangerousCommandBlocked) {
		t.Fatalf("error=%v", err)
	}
}

func TestUnmatchedCommandIsBlocked(t *testing.T) {
	engine := New()
	_, err := engine.Evaluate(testPolicy(pluginapi.OperationCommandExecuteReadonly), pluginapi.OperationCommandExecuteReadonly, map[string]any{"commands": []string{"unknown command"}})
	if !apperror.IsCode(err, apperror.CodeDangerousCommandBlocked) {
		t.Fatalf("error=%v", err)
	}
}

func TestRejectsControlCharactersAndLimits(t *testing.T) {
	engine := New()
	policy := testPolicy(pluginapi.OperationCommandExecuteReadonly)
	for _, command := range []string{"fake.show a\nnext", "fake.show a\x00next", strings.Repeat("x", policy.Limits.MaxCommandBytes+1)} {
		_, err := engine.Evaluate(policy, policy.Operation, map[string]any{"commands": []string{command}})
		if !apperror.IsCode(err, apperror.CodeValidationError) {
			t.Fatalf("command=%q error=%v", command, err)
		}
	}
	_, err := engine.Evaluate(policy, policy.Operation, map[string]any{"commands": []string{"fake.show a", "fake.show b", "fake.show c", "fake.show d"}})
	if !apperror.IsCode(err, apperror.CodeValidationError) {
		t.Fatalf("quantity error=%v", err)
	}
}

func TestHighRiskAndDurableJSONShape(t *testing.T) {
	engine := New()
	decision, err := engine.Evaluate(testPolicy(pluginapi.OperationCommandExecuteConfig), pluginapi.OperationCommandExecuteConfig, map[string]any{"commands": []any{"fake.set high-risk value"}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.RiskLevel != pluginapi.RiskHigh || len(decision.Commands) != 1 {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestRedactTranscriptDoesNotMutateSource(t *testing.T) {
	source := pluginapi.Transcript{Commands: []pluginapi.CommandRecord{{Output: "token=secret-token"}}}
	redacted := RedactTranscript(source, testPolicy(pluginapi.OperationCommandExecuteReadonly).OutputRedactions)
	if source.Commands[0].Output != "token=secret-token" {
		t.Fatal("source transcript was mutated")
	}
	if redacted.Commands[0].Output != "token=[REDACTED]" {
		t.Fatalf("redacted=%q", redacted.Commands[0].Output)
	}
}
