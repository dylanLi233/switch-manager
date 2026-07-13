package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/concurrency"
	"github.com/dylanLi233/switch-manager/internal/domain/audit"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/internal/pluginregistry"
	"github.com/dylanLi233/switch-manager/internal/scheduler"
	"github.com/dylanLi233/switch-manager/internal/testkit/fakecli"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
	fakeplugin "github.com/dylanLi233/switch-manager/plugins/fake"
)

const (
	operationUserID="00000000-0000-0000-0000-000000000401"
	operationCredentialID="00000000-0000-0000-0000-000000000402"
	operationDeviceID="00000000-0000-0000-0000-000000000403"
	operationRollbackTaskID="00000000-0000-0000-0000-000000000404"
)

type closableFakeSession struct{inner *fakecli.Session}
func(s *closableFakeSession)Execute(ctx context.Context,command pluginapi.PlannedCommand)(pluginapi.CommandOutput,error){return s.inner.Execute(ctx,command)}
func(s *closableFakeSession)Close()error{s.inner.Close();return nil}
type scriptedSessionFactory struct{mu sync.Mutex;scripts [][]fakecli.Step;opens int}
func(f *scriptedSessionFactory)add(steps ...fakecli.Step){f.mu.Lock();defer f.mu.Unlock();f.scripts=append(f.scripts,steps)}
func(f *scriptedSessionFactory)Open(context.Context,device.Device)(operationsvc.Session,error){f.mu.Lock();defer f.mu.Unlock();if len(f.scripts)==0{return nil,errors.New("no fake CLI script configured")};steps:=f.scripts[0];f.scripts=f.scripts[1:];f.opens++;session,err:=fakecli.New(steps,fakecli.Options{});if err!=nil{return nil,err};return &closableFakeSession{inner:session},nil}
func(f *scriptedSessionFactory)openCount()int{f.mu.Lock();defer f.mu.Unlock();return f.opens}

func TestOperationServicePostgreSQLIntegration(t *testing.T){
	dsn:=os.Getenv("TEST_DATABASE_DSN");if dsn==""{t.Skip("TEST_DATABASE_DSN is not set")}
	root,err:=filepath.Abs(filepath.Join("..","..",".."));if err!=nil{t.Fatal(err)};runMigration(t,root,dsn,"down","all");runMigration(t,root,dsn,"up")
	ctx:=context.Background();store,err:=Open(ctx,dsn);if err!=nil{t.Fatal(err)};defer store.Close()
	if _,err:=store.pool.Exec(ctx,`INSERT INTO users(id, external_subject, username) VALUES ($1::uuid,$2,$3)`,operationUserID,"operation-user","operation-user");err!=nil{t.Fatal(err)}
	now:=time.Now().UTC().Truncate(time.Microsecond);repos:=store.Repositories()
	if _,err:=repos.Credentials.Create(ctx,credential.Credential{ID:operationCredentialID,Name:"operation-credential",Type:credential.TypePassword,Username:"admin",EncryptedSecret:[]byte("encrypted"),KeyVersion:"v1",CreatedAt:now,UpdatedAt:now});err!=nil{t.Fatal(err)}
	if _,err:=repos.Devices.Create(ctx,device.Device{ID:operationDeviceID,Name:"operation-switch",Host:"192.0.2.40",SSHPort:22,CredentialID:operationCredentialID,Vendor:device.VendorHuawei,Model:"FAKE-SW",OSVersion:"1.0",DetectMode:device.DetectModeAuto,IdentityStatus:device.IdentityVerified,Status:device.StatusActive,CreatedAt:now,UpdatedAt:now});err!=nil{t.Fatal(err)}
	committer,err:=NewOperationSubmission(store);if err!=nil{t.Fatal(err)}
	_,err=committer.CreateTaskAndAudit(ctx,task.Persisted{Task:task.Task{ID:operationRollbackTaskID,Type:task.TypeOperation,Operation:"diagnostic.echo",TargetType:"switch",TargetID:operationDeviceID,Status:task.StatusPending,ExecutionMode:operation.ExecutionModeAsync,Payload:json.RawMessage(`{"test":true}`),CreatedBy:operationUserID,CreatedAt:now,Version:1}},audit.Record{ID:operationRollbackTaskID,RequestID:"req-invalid-audit",TaskID:operationRollbackTaskID,ActorUserID:operationUserID,ActorUsername:"operation-user",ActorRole:"INVALID",Action:"diagnostic.echo",TargetType:"switch",TargetID:operationDeviceID,Status:"PENDING",CreatedAt:now})
	if !apperror.IsCode(err,apperror.CodeAuditUnavailable){t.Fatalf("atomic preflight error=%v",err)}
	if _,err:=repos.Tasks.Get(ctx,operationRollbackTaskID);!apperror.IsCode(err,apperror.CodeTaskNotFound){t.Fatalf("rolled-back task error=%v",err)}

	registry:=pluginregistry.NewCurrent();plugin,_:=fakeplugin.New(pluginapi.VendorHuawei);if err:=registry.Register(plugin);err!=nil{t.Fatal(err)}
	planner,err:=operationsvc.NewPlanner(repos.Devices,registry);if err!=nil{t.Fatal(err)}
	factory:=&scriptedSessionFactory{};guards,_:=concurrency.NewController(5);executor,err:=operationsvc.NewExecutor(planner,repos.Audits,factory,guards);if err!=nil{t.Fatal(err)}
	dispatcher,err:=scheduler.New(repos.Tasks,executor,scheduler.Config{Workers:1,PollInterval:5*time.Millisecond});if err!=nil{t.Fatal(err)}
	runCtx,stop:=context.WithCancel(context.Background());done:=make(chan error,1);go func(){done<-dispatcher.Run(runCtx)}();defer func(){stop();if err:=<-done;err!=nil{t.Fatalf("scheduler run: %v",err)}}()
	service,err:=operationsvc.NewService(repos.Tasks,repos.Audits,planner,dispatcher,committer,operationsvc.Config{SyncWaitTimeout:3*time.Second,WaitPollInterval:5*time.Millisecond});if err!=nil{t.Fatal(err)}
	actor:=auth.Actor{UserID:operationUserID,Username:"operation-user",Role:auth.RoleAdmin,SourceIP:"192.0.2.1"}
	factory.add(fakecli.Step{Expect:pluginapi.PlannedCommand{Sequence:1,Text:`fake.echo.config "hello"`,Timeout:2*time.Second},Output:"configured"},fakecli.Step{Expect:pluginapi.PlannedCommand{Sequence:1,Text:"fake.config.save",Timeout:2*time.Second},Output:"partial",Failure:errors.New("save rejected"),ErrorCode:string(apperror.CodeCommandRejected)})
	configSubmission,err:=service.Submit(ctx,operationsvc.SubmitRequest{RequestID:"req-operation-config",Operation:operation.Request{Name:operation.Name(fakeplugin.OperationEchoConfig),Class:operation.ClassConfig,DeviceID:operationDeviceID,Parameters:map[string]any{"message":"hello"},ExecutionMode:operation.ExecutionModeSync,SaveConfig:true,Actor:actor}});if err!=nil{t.Fatal(err)}
	if !configSubmission.Completed||configSubmission.Task.Status!=task.StatusPartialSuccess||configSubmission.Task.ErrorCode!=string(apperror.CodeConfigSaveFailed){t.Fatalf("config task=%+v",configSubmission.Task)}
	configAudit,err:=repos.Audits.Get(ctx,configSubmission.AuditID);if err!=nil||configAudit.Status!=string(task.StatusPartialSuccess)||configAudit.ErrorCode!=string(apperror.CodeConfigSaveFailed){t.Fatalf("config audit=%+v err=%v",configAudit,err)}
	opensBeforeDryRun:=factory.openCount();dryRunSubmission,err:=service.Submit(ctx,operationsvc.SubmitRequest{RequestID:"req-operation-dry",Operation:operation.Request{Name:operation.Name(fakeplugin.OperationEchoConfig),Class:operation.ClassConfig,DeviceID:operationDeviceID,Parameters:map[string]any{"message":"dry"},ExecutionMode:operation.ExecutionModeSync,DryRun:true,Actor:actor}});if err!=nil{t.Fatal(err)}
	if !dryRunSubmission.Completed||dryRunSubmission.Task.Status!=task.StatusSuccess||factory.openCount()!=opensBeforeDryRun{t.Fatalf("dry task=%+v opens=%d",dryRunSubmission.Task,factory.openCount())}
	var dryResult map[string]any;if err:=json.Unmarshal(dryRunSubmission.Task.Result,&dryResult);err!=nil||dryResult["dry_run"]!=true{t.Fatalf("dry result=%s err=%v",dryRunSubmission.Task.Result,err)}
	factory.add(fakecli.Step{Expect:pluginapi.PlannedCommand{Sequence:1,Text:`fake.echo.query "hello"`,Timeout:2*time.Second},Output:"hello"})
	querySubmission,err:=service.Submit(ctx,operationsvc.SubmitRequest{RequestID:"req-operation-query",Operation:operation.Request{Name:operation.Name(fakeplugin.OperationEchoQuery),Class:operation.ClassQuery,DeviceID:operationDeviceID,Parameters:map[string]any{"message":"hello"},ExecutionMode:operation.ExecutionModeAsync,Actor:actor}});if err!=nil{t.Fatal(err)}
	queryTask:=waitOperationTaskStatus(t,repos.Tasks,querySubmission.Task.ID,task.StatusSuccess);if queryTask.PluginName!="fake-huawei"||queryTask.PluginVersion!="1.1.0"{t.Fatalf("query task=%+v",queryTask)}
}

func waitOperationTaskStatus(t *testing.T,repository *TaskRepository,id string,want task.Status)task.Persisted{t.Helper();deadline:=time.Now().Add(3*time.Second);for{value,err:=repository.Get(context.Background(),id);if err!=nil{t.Fatal(err)};if value.Status==want{return value};if time.Now().After(deadline){t.Fatalf("task %s status=%s want=%s",id,value.Status,want)};time.Sleep(5*time.Millisecond)}}
