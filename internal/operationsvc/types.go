package operationsvc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/audit"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

const SaveConfigOperation pluginapi.OperationName = "config.save"

type Config struct { SyncWaitTimeout time.Duration; WaitPollInterval time.Duration; RequireQueryAudit bool }
func (c Config) withDefaults() Config { if c.SyncWaitTimeout<=0 { c.SyncWaitTimeout=10*time.Second }; if c.WaitPollInterval<=0 { c.WaitPollInterval=20*time.Millisecond }; return c }

type SubmitRequest struct { RequestID string; Operation operation.Request }
func (r SubmitRequest) Validate() error { if strings.TrimSpace(r.RequestID)=="" { return errors.New("request ID is required") }; return r.Operation.Validate() }

type Submission struct { Task task.Persisted; AuditID string; Completed bool; Deferred bool; IdempotentReplay bool }

type PreflightCommitter interface { CreateTaskAndAudit(context.Context, task.Persisted, audit.Record) (task.Persisted, error) }
type Queue interface { Queue(context.Context, string) (task.Persisted, error) }
type PluginRegistry interface { Resolve(pluginapi.Vendor) (pluginapi.Plugin,error); LookupCapability(context.Context,pluginapi.Vendor,pluginapi.DeviceInfo,pluginapi.OperationName)(pluginapi.Capability,error) }
type Session interface { pluginapi.CLISession; Close() error }
type SessionFactory interface { Open(context.Context, device.Device) (Session,error) }
type IDGenerator func()(string,error)

func randomUUID()(string,error){var bytes [16]byte;if _,err:=rand.Read(bytes[:]);err!=nil{return "",err};bytes[6]=(bytes[6]&0x0f)|0x40;bytes[8]=(bytes[8]&0x3f)|0x80;encoded:=make([]byte,36);hex.Encode(encoded[0:8],bytes[0:4]);encoded[8]='-';hex.Encode(encoded[9:13],bytes[4:6]);encoded[13]='-';hex.Encode(encoded[14:18],bytes[6:8]);encoded[18]='-';hex.Encode(encoded[19:23],bytes[8:10]);encoded[23]='-';hex.Encode(encoded[24:36],bytes[10:16]);return string(encoded),nil}

type AuditFailureReporter interface { ReportAuditFailure(context.Context,string,error) }
type AuditFailureReporterFunc func(context.Context,string,error)
func(f AuditFailureReporterFunc)ReportAuditFailure(ctx context.Context,phase string,err error){if f!=nil{f(ctx,phase,err)}}
