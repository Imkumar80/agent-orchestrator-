package tui

type Screen int

const (
	ScreenDashboard Screen = iota
	ScreenNewTask
	ScreenSessions
	ScreenProviders
	ScreenSettings
	ScreenSessionDetail
)

var navigation = []struct {
	Screen Screen
	Label  string
}{
	{ScreenDashboard, "Dashboard"},
	{ScreenNewTask, "New Task"},
	{ScreenSessions, "Sessions"},
	{ScreenProviders, "Providers"},
	{ScreenSettings, "Settings"},
}

func (s Screen) String() string {
	if s == ScreenSessionDetail {
		return "Session Detail"
	}
	for _, item := range navigation {
		if item.Screen == s {
			return item.Label
		}
	}
	return "Dashboard"
}
