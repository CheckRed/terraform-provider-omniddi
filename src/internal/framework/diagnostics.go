package framework

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

func AddClientError(diags *diag.Diagnostics, summary string, err error) {
	diags.AddError(summary, ErrorDetail(err))
}

// Deprecated: use CreateFailedAfterSave or UpdateFailedAfterWrite, which also keep the state consistent.
func AddSavedStateError(diags *diag.Diagnostics, summary string, err error) {
	detail := ErrorDetail(err) + "\n\nThe object was saved to the Terraform state before this failure, so Terraform marks it tainted " +
		"and replaces it on the next `terraform apply`. A replacement is refused while deletion_protection is true."
	diags.AddError(summary, detail)
}

func ErrorDetail(err error) string {
	var (
		apiErr      *client.APIError
		approval    *client.ApprovalRequiredError
		untracked   *client.ApprovalUntrackedError
		rejected    *client.ApprovalRejectedError
		taskFailed  *client.TaskFailedError
		noTasks     *client.NoTasksError
		timeout     *client.TimeoutError
		missing     *client.MissingTaskHandleError
		unknownTags *client.UnknownTagsError
		gqlErr      *client.GraphQLError
		authErr     *client.AuthError

		pendingChange *client.PendingChangeError
	)
	switch {
	case errors.As(err, &pendingChange):
		return fmt.Sprintf("Another change to this object is pending approval (request %d). "+
			"Approve or reject request %d in OmniDDI, then run `terraform apply` again.", pendingChange.RequestID, pendingChange.RequestID)
	case errors.As(err, &approval):
		return approvalRequiredDetail(approval)
	case errors.As(err, &untracked):
		return "The change needs approval but OmniDDI did not return a request id, so the provider cannot track it. " +
			"Approve the pending request in OmniDDI, then run `terraform apply` again.\n\nBackend message: " + untracked.Detail + requestSuffix(untracked.HTTPRequestID)
	case errors.As(err, &rejected):
		return fmt.Sprintf("Approval request %d ended as %q, so the change was not applied.", rejected.RequestID, rejected.Status)
	case errors.As(err, &taskFailed):
		return fmt.Sprintf("OmniDDI accepted the change but agent task %d failed (correlation id %s).\n\nTask logs: %s",
			taskFailed.TaskID, taskFailed.CorrelationID, orNone(taskFailed.Logs))
	case errors.As(err, &noTasks):
		return fmt.Sprintf("OmniDDI accepted the change but no agent tasks appeared for correlation id %s, so it cannot be confirmed as applied.", noTasks.CorrelationID)
	case errors.As(err, &timeout):
		return timeoutDetail(timeout)
	case errors.As(err, &missing):
		return missing.Error() + ". The backend response was unexpected; refusing to treat it as success."
	case errors.As(err, &unknownTags):
		return unknownTags.Error()
	case errors.As(err, &apiErr):
		return apiErrorDetail(apiErr)
	case errors.As(err, &gqlErr):
		return gqlErr.Error()
	case errors.As(err, &authErr):
		return loginFailureDetail(authErr)
	}
	return err.Error()
}

func approvalRequiredDetail(e *client.ApprovalRequiredError) string {
	next := fmt.Sprintf("Approve request %d in OmniDDI, then run `terraform apply` again.", e.RequestID)
	if e.Adopts {
		next = fmt.Sprintf("Approve request %d in OmniDDI, then run `terraform apply` again; the provider adopts the approved object.", e.RequestID)
	}
	return fmt.Sprintf("Approval required (request %d). %s Alternatively set approval_mode = \"wait\" to wait for the approval during apply."+
		"\n\nBackend message: %s%s", e.RequestID, next, e.Detail, requestSuffix(e.HTTPRequestID))
}

func timeoutDetail(t *client.TimeoutError) string {
	var b strings.Builder
	b.WriteString(t.Error())
	switch {
	case t.CorrelationID != "":
		fmt.Fprintf(&b, "\n\nCorrelation id: %s. The change may still complete in OmniDDI; check Agent Tasks.", t.CorrelationID)
	case t.RequestID != 0:
		fmt.Fprintf(&b, "\n\nApproval request id: %d. The request is still pending or unapplied in OmniDDI.", t.RequestID)
	}
	return b.String()
}

func apiErrorDetail(e *client.APIError) string {
	var b strings.Builder
	fmt.Fprintf(&b, "OmniDDI returned HTTP %d for %s %s.\n\nBackend message: %s", e.StatusCode, e.Method, e.Path, orNone(e.Detail))
	if e.Code != "" {
		fmt.Fprintf(&b, "\nCode: %s", e.Code)
	}
	if hint := authHint(e); hint != "" {
		b.WriteString("\n\n")
		b.WriteString(hint)
	}
	b.WriteString(requestSuffix(e.RequestID))
	return b.String()
}

func loginFailureDetail(e *client.AuthError) string {
	detail := "Authentication failed: " + e.Error()
	switch e.StatusCode {
	case http.StatusUnauthorized:
		detail += "\n\nCheck username and password (or OMNIDDI_USERNAME and OMNIDDI_PASSWORD), and that the account is active."
	case http.StatusTooManyRequests:
		detail += "\n\nOmniDDI limits sign-ins per IP address. Wait a minute and retry, or run fewer parallel Terraform jobs from one host."
	}
	return detail
}

func authHint(e *client.APIError) string {
	detail := strings.ToLower(e.Detail)
	switch {
	case e.StatusCode == http.StatusUnauthorized:
		return "OmniDDI rejected the session even after signing in again. Check that the account is still active " +
			"and that its password has not changed."
	case e.StatusCode == http.StatusBadRequest && strings.Contains(detail, "network space"):
		return "Check network_space_id (or OMNIDDI_NETWORK_SPACE_ID): it must be the id of an existing network space."
	case e.StatusCode == http.StatusForbidden && strings.Contains(detail, "default network space"):
		return "The Default network space is not available to the account. Set network_space_id to a space the account can use."
	case e.StatusCode == http.StatusForbidden && strings.Contains(detail, "network space"):
		return "The account cannot use the network space set in network_space_id. Choose a space the account can use, " +
			"or leave network_space_id unset for its landing space."
	case e.StatusCode == http.StatusForbidden:
		return "The account lacks the OmniDDI permission for this action. Terraform acts with the roles of the account it signs in as."
	}
	return ""
}

func requestSuffix(id string) string {
	if id == "" {
		return ""
	}
	return "\nX-Request-ID: " + id
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
