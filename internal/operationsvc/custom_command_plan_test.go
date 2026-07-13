package operationsvc

import (
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/commandsecurity"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func approvedCommandDecision() commandsecurity.Decision {
	return commandsecurity.Decision{
		Operation: pluginapi.OperationCommandExecuteReadonly,
		Class: pluginapi.ClassQuery,
		RiskLevel: pluginapi.RiskLow,
		Limits: pluginapi.CustomCommandLimits{
			MaxCommands: 2, MaxCommandBytes: 64, MaxTotalBytes: 128,
			MaxCommandTimeout: 2 * time.Second, MaxTotalTimeout: 3 * time.Second,
			MaxOutputBytes: 1024,
		},
		Commands: []commandsecurity.CommandDecision{
			{Text: "fake.show one", RiskLevel: pluginapi.RiskLow},
			{Text: "fake.show two", RiskLevel: pluginapi.RiskLow},
		},
	}
}

func approvedCommandPlan() pluginapi.ExecutionPlan {
	return pluginapi.ExecutionPlan{
		Operation: pluginapi.OperationCommandExecuteReadonly,
		Class: pluginapi.ClassQuery,
		Commands: []pluginapi.PlannedCommand{
			{Sequence: 1, Text: "fake.show one", Timeout: time.Second, Sensitive: true},
			{Sequence: 2, Text: "fake.show two", Timeout: time.Second, Sensitive: true},
		},
	}
}

func TestValidateCustomCommandPlanAcceptsExactSequence(t *testing.T) {
	if err := validateCustomCommandPlan(approvedCommandPlan(), approvedCommandDecision()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCustomCommandPlanRejectsPluginChanges(t *testing.T) {
	tests := map[string]func(*pluginapi.ExecutionPlan){
		"changed text": func(plan *pluginapi.ExecutionPlan) { plan.Commands[1].Text = "fake.show changed" },
		"inserted command": func(plan *pluginapi.ExecutionPlan) { plan.Commands = append(plan.Commands, pluginapi.PlannedCommand{Sequence: 3, Text: "fake.show extra", Timeout: time.Second}) },
		"config mode": func(plan *pluginapi.ExecutionPlan) { plan.EnterConfigMode = true },
		"command timeout": func(plan *pluginapi.ExecutionPlan) { plan.Commands[0].Timeout = 3 * time.Second },
		"total timeout": func(plan *pluginapi.ExecutionPlan) { plan.Commands[0].Timeout = 2 * time.Second; plan.Commands[1].Timeout = 2 * time.Second },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			plan := approvedCommandPlan()
			mutate(&plan)
			if err := validateCustomCommandPlan(plan, approvedCommandDecision()); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
