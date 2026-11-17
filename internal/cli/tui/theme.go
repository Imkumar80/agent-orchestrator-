package tui

import "charm.land/lipgloss/v2"

type theme struct {
	brand, accent, muted, success, warning, danger lipgloss.Style
	panel, selected, title, dim                    lipgloss.Style
}

func newTheme() theme {
	return theme{
		brand:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#72D5C5")),
		accent:   lipgloss.NewStyle().Foreground(lipgloss.Color("#91A8FF")),
		muted:    lipgloss.NewStyle().Foreground(lipgloss.Color("#8290A5")),
		success:  lipgloss.NewStyle().Foreground(lipgloss.Color("#72D5A5")),
		warning:  lipgloss.NewStyle().Foreground(lipgloss.Color("#E7C779")),
		danger:   lipgloss.NewStyle().Foreground(lipgloss.Color("#F08080")),
		panel:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#344154")).Padding(0, 1),
		selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E7F6F3")).Background(lipgloss.Color("#263B46")).Padding(0, 1),
		title:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#DDE7F2")),
		dim:      lipgloss.NewStyle().Foreground(lipgloss.Color("#8290A5")),
	}
}
