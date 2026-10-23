package authz

// These permissions govern interactive administration. They are intentionally
// excluded from PAT scopes; existing token boundaries remain unchanged.
var (
	OperationsRead         = Permission{Resource: "operations", Action: ActionRead}
	ModelProfitsRead       = Permission{Resource: "model_profits", Action: ActionRead}
	BalanceLedgerRead      = Permission{Resource: "balance_ledger", Action: ActionRead}
	UpstreamTopupsRead     = Permission{Resource: "upstream_topups", Action: ActionRead}
	UpstreamTopupsWrite    = Permission{Resource: "upstream_topups", Action: ActionWrite}
	UserActivityRead       = Permission{Resource: "user_activity", Action: ActionRead}
	VideoUsageRead         = Permission{Resource: "video_usage", Action: ActionRead}
	MediaOperationsRead    = Permission{Resource: "media_operations", Action: ActionRead}
	ClientModelConfigRead  = Permission{Resource: "client_model_config", Action: ActionRead}
	ClientModelConfigWrite = Permission{Resource: "client_model_config", Action: ActionWrite}
	CreationContentRead    = Permission{Resource: "creation_content", Action: ActionRead}
	CreationContentWrite   = Permission{Resource: "creation_content", Action: ActionWrite}
	CustomerServiceRead    = Permission{Resource: "customer_service", Action: ActionRead}
	CustomerServiceWrite   = Permission{Resource: "customer_service", Action: ActionWrite}
	AnnouncementsRead      = Permission{Resource: "announcements", Action: ActionRead}
	AnnouncementsWrite     = Permission{Resource: "announcements", Action: ActionWrite}
	DiagnosticsRead        = Permission{Resource: "diagnostics", Action: ActionRead}
)

func init() {
	for _, resource := range []struct {
		key, label, group, description, editDescription string
	}{
		{"operations", "Business overview", "finance", "View operating totals and usage trends.", ""},
		{"model_profits", "Model profits", "finance", "View model revenue, procurement costs and profits without changing prices.", ""},
		{"balance_ledger", "Balance ledger", "finance", "View registration credits, top-ups and administrator balance changes.", ""},
		{"upstream_topups", "Upstream top-ups", "finance", "View upstream account top-up records.", "Add, edit and delete upstream top-up records."},
		{"user_activity", "User activity", "users", "View client activity, sessions and session details.", ""},
		{"video_usage", "Video statistics", "media", "View video requests, task statistics and usage analysis.", ""},
		{"media_operations", "Task monitoring and concurrency view", "media", "View image and video tasks and concurrency capacity. Only super administrators can change concurrency settings.", ""},
		{"client_model_config", "Client model configuration", "content", "View client model ordering, descriptions and tags.", "Edit client model defaults, ordering, descriptions and tags."},
		{"creation_content", "Creation content", "content", "View creation templates and sample content.", "Edit creation templates and upload sample images or videos."},
		{"customer_service", "Customer service", "content", "View customer service QR code settings.", "Upload a customer service QR code and edit its display settings."},
		{"announcements", "Announcements", "content", "View announcement drafts and publication settings.", "Edit announcements and their publication settings without accessing other system settings."},
		{"diagnostics", "Logs and diagnostics", "media", "View administrative request logs, error details and diagnostic statistics.", ""},
	} {
		roles := []string{BuiltInRoleAdmin}
		// Announcements were previously root-only system settings. Make the
		// narrow capability assignable, without automatically granting it.
		if resource.key == "announcements" {
			roles = nil
		}
		actions := []ActionDefinition{{Action: ActionRead, LabelKey: "View", DescriptionKey: resource.description, DefaultRoles: roles}}
		if resource.editDescription != "" {
			actions = append(actions, ActionDefinition{Action: ActionWrite, LabelKey: "Edit", DescriptionKey: resource.editDescription, DefaultRoles: roles})
		}
		RegisterResource(ResourceDefinition{Resource: resource.key, LabelKey: resource.label, GroupKey: resource.group, DescriptionKey: resource.description, SessionOnly: true, Actions: actions})
	}
}
