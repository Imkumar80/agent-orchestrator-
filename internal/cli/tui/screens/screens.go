package screens

// PlaceholderContent keeps navigation usable while later phases add each
// screen's API-backed workflow.
func PlaceholderContent(name string) string {
	switch name {
	case "New Task":
		return "Task creation will be available here in a later phase.\n\nUse the existing CLI command for now:\n  agent-orchestrator task create --description TEXT --workdir DIR"
	case "Sessions":
		return "Session browsing is coming next. The dashboard currently shows recent sessions."
	case "Providers":
		return "Provider details are coming next. Availability is summarized on the dashboard."
	case "Settings":
		return "Local TUI preferences are coming next. Server URL is controlled by --server or ORCHESTRATOR_SERVER_URL."
	default:
		return ""
	}
}
