package commandsecurity

import (
	"context"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

// WrapPlugin returns a core-owned wrapper that applies output limits and
// redaction before a custom command transcript reaches vendor parsing code.
func WrapPlugin(base pluginapi.Plugin, decision Decision) pluginapi.Plugin {
	return &securedPlugin{Plugin: base, decision: decision}
}

type securedPlugin struct {
	pluginapi.Plugin
	decision Decision
}

func (p *securedPlugin) ParseResult(ctx context.Context, plan pluginapi.ExecutionPlan, transcript pluginapi.Transcript) (pluginapi.OperationResult, error) {
	if plan.Operation != p.decision.Operation {
		return p.Plugin.ParseResult(ctx, plan, transcript)
	}
	total := 0
	for _, record := range transcript.Commands {
		total += len(record.Output)
		if total > p.decision.Limits.MaxOutputBytes {
			commands := make([]pluginapi.CommandExecution, len(transcript.Commands))
			for index, item := range transcript.Commands {
				commands[index] = pluginapi.CommandExecution{
					Sequence: item.Sequence, Succeeded: item.Succeeded,
					OutputTruncated: true, ErrorCode: item.ErrorCode, Duration: item.Duration,
				}
			}
			return pluginapi.OperationResult{
				Status: pluginapi.ResultFailed, Commands: commands,
				ErrorCode: string(apperror.CodeResultTooLarge),
				ErrorMessage: "custom command output exceeds the configured limit",
				StartedAt: transcript.StartedAt, FinishedAt: transcript.FinishedAt,
			}, nil
		}
	}
	return p.Plugin.ParseResult(ctx, plan, RedactTranscript(transcript, p.decision.OutputRedactions))
}

var _ pluginapi.Plugin = (*securedPlugin)(nil)
