package framework

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

const (
	appliedAdoptionWindow = 7 * 24 * time.Hour
	recoveryTimeout       = 30 * time.Second
)

type WriteOp int

const (
	WriteOpAuto WriteOp = iota
	WriteOpCreate
	WriteOpUpdate
	WriteOpDelete
)

type WriteSpec struct {
	Method     string
	Path       string
	Body       any
	ActionType string
	Match      func(details map[string]any) bool
	Op         WriteOp
	Recover    func(ctx context.Context) (id int64, found bool, err error)
	Precheck   func(ctx context.Context) error

	RecoverOnDuplicate bool
}

func (s WriteSpec) op() WriteOp {
	if s.Op != WriteOpAuto {
		return s.Op
	}
	switch s.Method {
	case http.MethodPost:
		return WriteOpCreate
	case http.MethodDelete:
		return WriteOpDelete
	}
	return WriteOpUpdate
}

func (s WriteSpec) recoverable(err error) bool {
	if s.Recover == nil {
		return false
	}
	return client.IsAmbiguousWriteFailure(err) || s.RecoverOnDuplicate && client.IsDuplicate(err)
}

type WriteOutcome struct {
	Result            *client.WriteResult
	ApprovalRequestID int64
	Adopted           bool
	RecoveredID       int64
	recoveredFrom     error
}

func (o *WriteOutcome) ViaApproval() bool { return o.Result == nil }

func (o *WriteOutcome) ResolveID(ctx context.Context, byNaturalKey func(ctx context.Context) (int64, error), keys ...string) (int64, error) {
	if o.RecoveredID != 0 {
		return o.RecoveredID, nil
	}
	if id, ok := o.Result.Int(keys...); ok {
		return id, nil
	}
	if byNaturalKey == nil {
		return 0, errors.New("OmniDDI accepted the request but returned no object id")
	}
	id, err := byNaturalKey(ctx)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, errors.New("OmniDDI accepted the request but the object id could not be resolved")
	}
	return id, nil
}

func (o *WriteOutcome) Warnings() diag.Diagnostics {
	var diags diag.Diagnostics
	switch {
	case o == nil:
	case o.recoveredFrom != nil:
		diags.AddWarning("Adopted an object after the create request failed", fmt.Sprintf(
			"The create request failed (%s) but the object exists in OmniDDI, so the provider adopted it. "+
				"Whether it has been deployed to the servers could not be confirmed; check Agent Tasks in OmniDDI.", ErrorDetail(o.recoveredFrom)))
	case o.ApprovalRequestID != 0 && !o.Adopted:
		diags.AddWarning("Deployment of the approved change is not confirmed", fmt.Sprintf(
			"Request %d was approved and applied in OmniDDI. The agent tasks created by an approval cannot be tracked by the provider, "+
				"so deployment to the servers is not confirmed; check Agent Tasks in OmniDDI if the change does not take effect.", o.ApprovalRequestID))
	}
	return diags
}

type Lifecycle struct {
	Client *client.Client
	Mode   ApprovalMode
}

func NewLifecycle(pd *ProviderData, override types.String) Lifecycle {
	mode := pd.ApprovalMode
	if !override.IsNull() && !override.IsUnknown() && override.ValueString() != "" {
		mode = ApprovalMode(override.ValueString())
	}
	if mode == "" {
		mode = ApprovalModeFail
	}
	return Lifecycle{Client: pd.Client, Mode: mode}
}

func (l Lifecycle) Write(ctx context.Context, spec WriteSpec) (*WriteOutcome, error) {
	op := spec.op()
	if outcome, err := l.resumePending(ctx, spec, op == WriteOpUpdate); outcome != nil || err != nil {
		return outcome, err
	}
	if op == WriteOpCreate {
		if outcome, err := l.adoptApplied(ctx, spec); outcome != nil || err != nil {
			return outcome, err
		}
	}
	if spec.Precheck != nil {
		if err := spec.Precheck(ctx); err != nil {
			return nil, err
		}
	}
	result, err := l.Client.Write(ctx, spec.Method, spec.Path, spec.Body)
	var approval *client.ApprovalRequiredError
	var untracked *client.ApprovalUntrackedError
	switch {
	case err == nil:
		return &WriteOutcome{Result: result}, nil
	case errors.As(err, &approval):
		approval.Adopts = op == WriteOpCreate
		return l.awaitOrFail(ctx, approval.RequestID, err)
	case errors.As(err, &untracked) && l.Mode == ApprovalModeWait:
		if outcome, findErr := l.resumePending(ctx, spec, false); outcome != nil || findErr != nil {
			return outcome, findErr
		}
	case op == WriteOpCreate && spec.recoverable(err):
		if outcome := l.recoverCreate(ctx, spec, err); outcome != nil {
			return outcome, nil
		}
	}
	return nil, err
}

func (l Lifecycle) resumePending(ctx context.Context, spec WriteSpec, conflict bool) (*WriteOutcome, error) {
	if spec.ActionType == "" || spec.Match == nil {
		return nil, nil
	}
	pending, err := l.Client.FindPendingRequests(ctx, spec.ActionType, spec.adoptionMatch())
	if err != nil {
		tflog.Warn(l.Client.MaskedContext(ctx), "duplicate approval lookup skipped", map[string]any{"error": err.Error()})
		return nil, ctx.Err()
	}
	if len(pending) == 0 {
		return nil, nil
	}
	if conflict {
		return nil, &client.PendingChangeError{RequestID: pending[0].ID, Method: spec.Method, Path: spec.Path}
	}
	return l.awaitOrFail(ctx, pending[0].ID, &client.ApprovalRequiredError{
		RequestID: pending[0].ID,
		Detail:    "an identical change is already pending approval",
		Method:    spec.Method,
		Path:      spec.Path,
		Adopts:    spec.op() == WriteOpCreate,
	})
}

func (l Lifecycle) adoptApplied(ctx context.Context, spec WriteSpec) (*WriteOutcome, error) {
	if spec.ActionType == "" || spec.Match == nil {
		return nil, nil
	}
	applied, err := l.Client.FindRecentlyAppliedRequests(ctx, spec.ActionType, appliedAdoptionWindow, spec.adoptionMatch())
	if err != nil {
		tflog.Debug(l.Client.MaskedContext(ctx), "applied approval lookup skipped", map[string]any{"error": err.Error()})
		return nil, ctx.Err()
	}
	if len(applied) == 0 {
		return nil, nil
	}
	outcome := &WriteOutcome{ApprovalRequestID: applied[0].ID, Adopted: true}
	if spec.Recover == nil {
		return outcome, nil
	}
	id, found, err := spec.Recover(ctx)
	switch {
	case err != nil:
		tflog.Warn(l.Client.MaskedContext(ctx), "adopting an applied request failed", map[string]any{"error": err.Error()})
		return nil, ctx.Err()
	case !found:
		return nil, nil
	}
	outcome.RecoveredID = id
	return outcome, nil
}

func (l Lifecycle) AppliedRecently(ctx context.Context, actionType string, match func(details map[string]any) bool) (int64, error) {
	applied, err := l.Client.FindRecentlyAppliedRequests(ctx, actionType, appliedAdoptionWindow, match)
	if err != nil || len(applied) == 0 {
		return 0, err
	}
	return applied[0].ID, nil
}

func (l Lifecycle) recoverCreate(ctx context.Context, spec WriteSpec, cause error) *WriteOutcome {
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil
	}
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), recoveryTimeout)
		defer cancel()
	}
	id, found, err := spec.Recover(ctx)
	if err != nil || !found {
		if err != nil {
			tflog.Warn(l.Client.MaskedContext(ctx), "recovering a failed create failed", map[string]any{"error": err.Error()})
		}
		return nil
	}
	return &WriteOutcome{RecoveredID: id, Adopted: true, recoveredFrom: cause}
}

func (l Lifecycle) awaitOrFail(ctx context.Context, requestID int64, pending error) (*WriteOutcome, error) {
	if l.Mode != ApprovalModeWait {
		return nil, pending
	}
	if _, err := l.Client.AwaitApproval(ctx, requestID); err != nil {
		return nil, err
	}
	return &WriteOutcome{ApprovalRequestID: requestID}, nil
}

func (l Lifecycle) Converge(ctx context.Context, outcome *WriteOutcome) error {
	if outcome == nil || outcome.Result == nil {
		return nil
	}
	return l.Client.AwaitWrite(ctx, outcome.Result)
}

func (l Lifecycle) AwaitGone(ctx context.Context, exists func(ctx context.Context) (bool, error)) error {
	poll := l.Client.PollSettings()
	interval := poll.InitialInterval
	waiting := "the object to be removed"
	for {
		present, err := exists(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
			return &client.TimeoutError{Waiting: waiting, Cause: ctx.Err()}
		case err != nil:
			return err
		case !present:
			return nil
		}
		if err := sleepCtx(ctx, interval); err != nil {
			return &client.TimeoutError{Waiting: waiting, Cause: err}
		}
		interval = min(interval*2, poll.MaxInterval)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func HandleReadError(ctx context.Context, resp *resource.ReadResponse, err error) (handled bool) {
	if err == nil {
		return false
	}
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return true
	}
	AddClientError(&resp.Diagnostics, "Unable to read the OmniDDI object", err)
	return true
}

func AppendIfErr(diags *diag.Diagnostics, summary string, err error) bool {
	if err == nil {
		return false
	}
	AddClientError(diags, summary, err)
	return true
}

func IgnoreNotFound(err error) error {
	if client.IsNotFound(err) {
		return nil
	}
	return err
}
