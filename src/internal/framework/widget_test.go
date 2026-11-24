package framework_test

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	omniprovider "github.com/CheckRed/terraform-provider-omniddi/internal/provider"
)

const widgetQuery = `query Widget($id: Int!) { widgetById(id: $id) { id name fqdn description } }`

const widgetByNameQuery = `query WidgetByName($name: String!) { widgetByName(name: $name) { id } }`

type widgetModel struct {
	ID                 types.Int64    `tfsdk:"id"`
	Name               types.String   `tfsdk:"name"`
	FQDN               framework.FQDN `tfsdk:"fqdn"`
	Description        types.String   `tfsdk:"description"`
	ApprovalMode       types.String   `tfsdk:"approval_mode"`
	DeletionProtection types.Bool     `tfsdk:"deletion_protection"`
	Tags               types.Map      `tfsdk:"tags"`
	TagsList           types.Map      `tfsdk:"tags_list"`
	TagsAll            types.Map      `tfsdk:"tags_all"`
	Timeouts           timeouts.Value `tfsdk:"timeouts"`
}

type widgetDTO struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	FQDN        *string `json:"fqdn"`
	Description *string `json:"description"`
}

type widgetResource struct {
	pd *framework.ProviderData
}

var (
	_ resource.ResourceWithConfigure   = (*widgetResource)(nil)
	_ resource.ResourceWithIdentity    = (*widgetResource)(nil)
	_ resource.ResourceWithImportState = (*widgetResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*widgetResource)(nil)
)

func newWidgetResource() resource.Resource { return &widgetResource{} }

func (r *widgetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_widget"
}

func (r *widgetResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = framework.IdentitySchema()
}

func (r *widgetResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		framework.AttrID:                 framework.IDAttribute(),
		framework.AttrApprovalMode:       framework.ApprovalModeAttribute(),
		framework.AttrDeletionProtection: framework.DeletionProtectionAttribute(),
		framework.AttrTimeouts:           framework.TimeoutsAttribute(ctx),
		"name": schema.StringAttribute{
			Required:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"fqdn":        schema.StringAttribute{Optional: true, CustomType: framework.FQDNType{}},
		"description": schema.StringAttribute{Optional: true},
	}
	for name, attr := range framework.TagsAttributes() {
		attrs[name] = attr
	}
	resp.Schema = schema.Schema{Attributes: attrs}
}

func (r *widgetResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.pd.ModifyTagsAllPlan(ctx, req, resp)
}

func (r *widgetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *widgetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	framework.ImportByIDOrKey(ctx, req, resp, func(ctx context.Context, key string) (int64, error) {
		return r.idByName(ctx, key)
	})
	resp.Diagnostics.Append(framework.ImportDefaults(ctx, &resp.State, framework.AttrDeletionProtection)...)
}

func (r *widgetResource) body(m widgetModel) map[string]any {
	body := map[string]any{"name": m.Name.ValueString(), "description": m.Description.ValueStringPointer()}
	if !m.FQDN.IsNull() {
		body["fqdn"] = m.FQDN.ValueString()
	}
	return body
}

func (r *widgetResource) fetch(ctx context.Context, id int64) (*widgetDTO, error) {
	var out struct {
		Widget *widgetDTO `json:"widgetById"`
	}
	if err := r.pd.Client.Query(ctx, widgetQuery, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Widget == nil {
		return nil, client.NewNotFound("widget")
	}
	return out.Widget, nil
}

func (r *widgetResource) idByName(ctx context.Context, name string) (int64, error) {
	var out struct {
		Widget *struct {
			ID int64 `json:"id"`
		} `json:"widgetByName"`
	}
	if err := r.pd.Client.Query(ctx, widgetByNameQuery, map[string]any{"name": name}, &out); err != nil {
		return 0, err
	}
	if out.Widget == nil {
		return 0, client.NewNotFound("widget")
	}
	return out.Widget.ID, nil
}

func (r *widgetResource) applyDTO(m *widgetModel, dto *widgetDTO) {
	m.Name = types.StringValue(dto.Name)
	m.Description = types.StringPointerValue(dto.Description)
	m.FQDN = framework.NewFQDNNull()
	if dto.FQDN != nil {
		m.FQDN = framework.NewFQDNValue(*dto.FQDN)
	}
}

func (r *widgetResource) refresh(ctx context.Context, m *widgetModel) error {
	dto, err := r.fetch(ctx, m.ID.ValueInt64())
	if err != nil {
		return err
	}
	r.applyDTO(m, dto)
	remote, err := r.pd.Client.GetTagValues(ctx, client.TagObjectDNSView, m.ID.ValueInt64())
	if err != nil {
		return err
	}
	tags, diags := r.pd.TagsFromRemote(ctx, remote, m.Tags, m.TagsList)
	if diags.HasError() {
		return fmt.Errorf("map tags: %s", diags)
	}
	m.Tags, m.TagsList, m.TagsAll = tags.Tags, tags.TagsList, tags.TagsAll
	return nil
}

func (r *widgetResource) settleAfterApply(ctx context.Context, m *widgetModel) error {
	dto, err := r.fetch(ctx, m.ID.ValueInt64())
	if err != nil {
		return err
	}
	r.applyDTO(m, dto)
	tags, diags := r.pd.TagsAfterApply(ctx, m.Tags, m.TagsList)
	if diags.HasError() {
		return fmt.Errorf("map tags: %s", diags)
	}
	m.Tags, m.TagsList, m.TagsAll = tags.Tags, tags.TagsList, tags.TagsAll
	return nil
}

func nameMatcher(name string) func(map[string]any) bool {
	return func(details map[string]any) bool { return details["name"] == name }
}

func (r *widgetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan widgetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpCreate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	lifecycle := framework.NewLifecycle(r.pd, plan.ApprovalMode)

	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPost, Path: "/widgets/", Body: r.body(plan),
		ActionType: "create_widget", Match: nameMatcher(plan.Name.ValueString()),
		Recover: r.recoverByName(plan.Name.ValueString()),
	})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to create widget", err) {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)

	id, err := outcome.ResolveID(ctx, func(ctx context.Context) (int64, error) { return r.idByName(ctx, plan.Name.ValueString()) }, "widget_id")
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to determine the widget id", err) {
		return
	}

	plan.ID = types.Int64Value(id)
	plan.TagsAll, _ = r.pd.PlannedTagsAll(ctx, plan.Tags)
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, id)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := lifecycle.Converge(ctx, outcome); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Widget was created but not confirmed", err)
		return
	}
	desired, diags := r.pd.DesiredTags(ctx, plan.Tags, plan.TagsList)
	resp.Diagnostics.Append(diags...)
	if err := r.pd.Client.ApplyTags(ctx, client.TagObjectDNSView, id, desired); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Widget tags could not be applied", err)
		return
	}
	if err := r.settleAfterApply(ctx, &plan); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Widget could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *widgetResource) recoverByName(name string) func(context.Context) (int64, bool, error) {
	return func(ctx context.Context) (int64, bool, error) {
		id, err := r.idByName(ctx, name)
		if client.IsNotFound(err) {
			return 0, false, nil
		}
		return id, err == nil, err
	}
}

func (r *widgetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state widgetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpRead, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	if err := r.refresh(ctx, &state); err != nil {
		framework.HandleReadError(ctx, resp, err)
		return
	}
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, state.ID.ValueInt64())...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *widgetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan widgetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpUpdate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	lifecycle := framework.NewLifecycle(r.pd, plan.ApprovalMode)
	id := plan.ID.ValueInt64()

	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPut, Path: "/widgets/" + strconv.FormatInt(id, 10), Body: r.body(plan),
		ActionType: "update_widget", Match: func(d map[string]any) bool { return d["widget_id"] == float64(id) },
	})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to update widget", err) {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	if err := lifecycle.Converge(ctx, outcome); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Widget update was not confirmed", err)
		return
	}
	desired, diags := r.pd.DesiredTags(ctx, plan.Tags, plan.TagsList)
	resp.Diagnostics.Append(diags...)
	if err := r.pd.Client.ApplyTags(ctx, client.TagObjectDNSView, id, desired); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Widget tags could not be applied", err)
		return
	}
	if err := r.settleAfterApply(ctx, &plan); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Widget could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *widgetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state widgetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !framework.CheckDeletionProtection(&resp.Diagnostics, "omniddi_widget", state.DeletionProtection) {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpDelete, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	lifecycle := framework.NewLifecycle(r.pd, state.ApprovalMode)
	id := state.ID.ValueInt64()

	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodDelete, Path: "/widgets/" + strconv.FormatInt(id, 10),
		ActionType: "delete_widget", Match: func(d map[string]any) bool { return d["widget_id"] == float64(id) },
	})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to delete widget", framework.IgnoreNotFound(err)) || outcome == nil {
		return
	}
	if framework.AppendIfErr(&resp.Diagnostics, "Widget deletion was not confirmed", lifecycle.Converge(ctx, outcome)) {
		return
	}
	err = lifecycle.AwaitGone(ctx, func(ctx context.Context) (bool, error) {
		_, fetchErr := r.fetch(ctx, id)
		if client.IsNotFound(fetchErr) {
			return false, nil
		}
		return fetchErr == nil, fetchErr
	})
	framework.AppendIfErr(&resp.Diagnostics, "Widget still exists after deletion", err)
}

type widgetProvider struct {
	*omniprovider.OmniDDIProvider
}

func (widgetProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{newWidgetResource}
}

func widgetFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	base, _ := omniprovider.New("test")().(*omniprovider.OmniDDIProvider)
	var p provider.Provider = widgetProvider{OmniDDIProvider: base}
	return acctest.ProviderFactoriesFor(p)
}

type widget struct {
	ID          int64
	Name        string
	FQDN        *string
	Description *string
}

type widgetBackend struct {
	srv       *fakeserver.Server
	mu        sync.Mutex
	widgets   map[int64]*widget
	taskFinal []string
	logs      string
	approval  func(body map[string]any) (fakeserver.Response, bool)
}

func newWidgetBackend(t *testing.T) *widgetBackend {
	t.Helper()
	srv := fakeserver.New(t, fakeserver.WithoutSchemaValidation())
	b := &widgetBackend{srv: srv, widgets: map[int64]*widget{}, taskFinal: []string{fakeserver.StatusCompleted}}
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 1, Name: "env"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 2, Name: "owner"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 3, Name: "teams", DataType: "LIST", MultiValue: true})

	srv.HandleREST(http.MethodPost, "/api/v1/widgets/", b.create)
	srv.HandleREST(http.MethodPut, "/api/v1/widgets/{id}", b.update)
	srv.HandleREST(http.MethodDelete, "/api/v1/widgets/{id}", b.remove)
	srv.HandleGraphQL("widgetById", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		id, _ := args["id"].(float64)
		w, ok := b.widgets[int64(id)]
		if !ok {
			return nil, nil
		}
		return widgetJSON(w), nil
	})
	srv.HandleGraphQL("widgetByName", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, w := range b.widgets {
			if w.Name == args["name"] {
				return map[string]any{"id": w.ID}, nil
			}
		}
		return nil, nil
	})
	return b
}

func widgetJSON(w *widget) map[string]any {
	return map[string]any{"id": w.ID, "name": w.Name, "fqdn": w.FQDN, "description": w.Description}
}

func (b *widgetBackend) tasks() fakeserver.TaskHandle {
	if b.logs != "" {
		return b.srv.StageTaskScript(b.logs, b.taskFinal...)
	}
	return b.srv.StageTasks(b.taskFinal...)
}

func (b *widgetBackend) apply(w *widget, body map[string]any) {
	w.Name, _ = body["name"].(string)
	w.FQDN, w.Description = nil, nil
	if v, ok := body["fqdn"].(string); ok {
		lowered := strings.ToLower(strings.TrimSuffix(v, "."))
		w.FQDN = &lowered
	}
	if v, ok := body["description"].(string); ok {
		w.Description = &v
	}
}

func (b *widgetBackend) create(r *fakeserver.Request) fakeserver.Response {
	body := r.Map()
	if b.approval != nil {
		if resp, pending := b.approval(body); pending {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	w := &widget{ID: b.srv.NextID()}
	b.apply(w, body)
	b.widgets[w.ID] = w
	return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(map[string]any{"widget_id": w.ID}))
}

func (b *widgetBackend) update(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	b.mu.Lock()
	defer b.mu.Unlock()
	w, ok := b.widgets[id]
	if !ok {
		return fakeserver.Detail(http.StatusNotFound, "Widget not found")
	}
	b.apply(w, r.Map())
	return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(map[string]any{"widget_id": id}))
}

func (b *widgetBackend) remove(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.widgets[id]; !ok {
		return fakeserver.Detail(http.StatusNotFound, "Widget not found")
	}
	delete(b.widgets, id)
	return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(nil))
}

func (b *widgetBackend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.widgets)
}
