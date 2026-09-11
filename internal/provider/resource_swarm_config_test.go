package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"terraform-provider-arcane/internal/sdkclient"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func configState(t *testing.T, m swarmConfigModel) tfsdk.State {
	t.Helper()
	var sresp resource.SchemaResponse
	(&SwarmConfigResource{}).Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	s := tfsdk.State{Schema: sresp.Schema}
	if diags := s.Set(context.Background(), &m); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return s
}

func configPlan(t *testing.T, m swarmConfigModel) tfsdk.Plan {
	t.Helper()
	var sresp resource.SchemaResponse
	(&SwarmConfigResource{}).Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	p := tfsdk.Plan{Schema: sresp.Schema}
	if diags := p.Set(context.Background(), &m); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return p
}

func fullConfigModel() swarmConfigModel {
	return swarmConfigModel{
		ID:            types.StringValue("cfg-1"),
		EnvironmentID: types.StringValue("env-1"),
		Name:          types.StringValue("app_config"),
		Data:          types.StringValue("greeting: hello"),
		Labels:        types.MapNull(types.StringType),
		VersionIndex:  types.Int64Value(10),
		CreatedAt:     types.StringValue("2026-01-01T00:00:00Z"),
		UpdatedAt:     types.StringValue("2026-01-01T00:00:00Z"),
	}
}

// TestSwarmConfigRead_PreservesNullLabels locks in that an unconfigured
// (null) optional-only labels attribute stays null on refresh, even when the
// server returns labels — otherwise the RequiresReplace label diff would force
// a spurious destroy/recreate.
func TestSwarmConfigRead_PreservesNullLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"id":"cfg-1","spec":{"Name":"app_config","Labels":{"com.docker.stack.namespace":"web"}},"version":{"Index":10},"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}}`))
	}))
	defer srv.Close()

	r := &SwarmConfigResource{client: sdkclient.NewClient(srv.URL, "k")}
	prior := fullConfigModel() // Labels null, Data set

	resp := &resource.ReadResponse{State: configState(t, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: configState(t, prior)}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}

	var got swarmConfigModel
	resp.State.Get(context.Background(), &got)
	if !got.Labels.IsNull() {
		t.Errorf("null labels adopted server labels (would force replace): got %v", got.Labels)
	}
	// Plaintext data is write-only on the API and must be preserved from state.
	if got.Data.ValueString() != "greeting: hello" {
		t.Errorf("data not preserved on read: got %q, want %q", got.Data.ValueString(), "greeting: hello")
	}
}

// TestSwarmConfigCreate_EncodesDataBase64 verifies the v2.0.1 wire contract:
// plaintext data is base64-encoded and the spec uses PascalCase field names.
func TestSwarmConfigCreate_EncodesDataBase64(t *testing.T) {
	var gotBody sdkclient.SwarmConfigCreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Write([]byte(`{"success":true,"data":{"id":"cfg-1","spec":{"Name":"app_config"},"version":{"Index":1},"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}}`))
	}))
	defer srv.Close()

	r := &SwarmConfigResource{client: sdkclient.NewClient(srv.URL, "k")}
	plan := fullConfigModel()
	plan.ID = types.StringNull()
	plan.VersionIndex = types.Int64Null()
	plan.CreatedAt = types.StringNull()
	plan.UpdatedAt = types.StringNull()

	resp := &resource.CreateResponse{State: configState(t, fullConfigModel())}
	r.Create(context.Background(), resource.CreateRequest{Plan: configPlan(t, plan)}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", resp.Diagnostics)
	}

	want := base64.StdEncoding.EncodeToString([]byte("greeting: hello"))
	if gotBody.Spec.Data != want {
		t.Errorf("config data not base64-encoded: got %q, want %q", gotBody.Spec.Data, want)
	}
	if gotBody.Spec.Name != "app_config" {
		t.Errorf("config spec Name not sent: got %q", gotBody.Spec.Name)
	}
}
