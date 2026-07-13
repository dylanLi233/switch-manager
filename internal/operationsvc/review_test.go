package operationsvc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/concurrency"
	"github.com/dylanLi233/switch-manager/internal/domain/audit"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

type mainFailureSession struct {
	mu       sync.Mutex
	commands []string
}

func (s *mainFailureSession) Execute(_ context.Context, command pluginapi.PlannedCommand) (pluginapi.CommandOutput, error) {
	s.mu.Lock()
	s.commands = append(s.commands, command.Text)
	s.mu.Unlock()
	if command.Text == "main" {
		return pluginapi.CommandOutput{Output: "rejected", Duration: time.Millisecond}, errors.New("main failed")
	}
	return pluginapi.CommandOutput{Output: "saved", Duration: time.Millisecond}, nil
}
func (s *mainFailureSession) Close() error { return nil }

type mainFailureFactory struct{ session *mainFailureSession }
func (f mainFailureFactory) Open(context.Context, device.Device) (Session, error) { return f.session, nil }

func TestMainFailureSkipsSaveConfig(t *testing.T) {
	planner, _, audits := setup(t)
	session := &mainFailureSession{}
	guards, _ := concurrency.NewController(5)
	executor, _ := NewExecutor(planner, audits, mainFailureFactory{session: session}, guards)
	snapshot := snapshotRequest(request(operation.ExecutionModeAsync, false, true).Operation)
	fingerprintValue, _ := fingerprint(snapshot)
	prepared, _ := planner.prepare(context.Background(), planInput{Name: snapshot.Name, Class: snapshot.Class, DeviceID: snapshot.DeviceID, Parameters: snapshot.Parameters, SaveConfig: true, MainPlanID: "main-plan", SavePlanID: "save-plan"})
	payload, _ := encodeTaskPayload(taskPayload{Version: payloadVersion, Request: snapshot, RequestFingerprint: fingerprintValue, RequestID: "req-main-failure", MainPlanID: "main-plan", MainPlanSHA256: prepared.MainHash, SavePlanID: "save-plan", SavePlanSHA256: prepared.SaveHash})
	value := task.Persisted{Task: task.Task{ID: "task-main-failure", Operation: snapshot.Name, TargetID: snapshot.DeviceID, ExecutionMode: snapshot.ExecutionMode, Payload: payload}, SaveConfig: true}
	audits.values[value.ID] = audit.Record{ID: value.ID}
	result, err := executor.Execute(context.Background(), value)
	if err != nil { t.Fatal(err) }
	if result.Status != task.StatusFailed || result.ErrorCode != string(apperror.CodeCommandRejected) { t.Fatalf("result=%+v", result) }
	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.commands) != 1 || session.commands[0] != "main" { t.Fatalf("commands=%v", session.commands) }
}

func TestQueryAuditFailureIsBestEffortByDefault(t *testing.T) {
	planner, tasks, audits := setup(t)
	audits.failCreate = true
	queue := &queueStub{repo: tasks}
	service := newTestService(tasks, audits, planner, queue, Config{})
	ids := []string{"query-task", "query-plan"}
	service.ids = func() (string, error) { id := ids[0]; ids = ids[1:]; return id, nil }
	reported := 0
	service.SetAuditFailureReporter(AuditFailureReporterFunc(func(context.Context, string, error) { reported++ }))
	query := request(operation.ExecutionModeAsync, false, false)
	query.Operation.Name = "diagnostic.echo"
	query.Operation.Class = operation.ClassQuery
	submission, err := service.Submit(context.Background(), query)
	if err != nil { t.Fatal(err) }
	if submission.Task.Status != task.StatusQueued || queue.calls != 1 || reported != 1 { t.Fatalf("submission=%+v calls=%d reported=%d", submission, queue.calls, reported) }
}
