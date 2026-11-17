package tui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Up, Down, Enter, NewTask, Refresh, Help, Back, Quit key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("up/down", "navigate")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("up/down", "navigate")),
		Enter:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		NewTask: key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new task")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Enter, k.NewTask, k.Refresh, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.Enter, k.NewTask}, {k.Refresh, k.Help, k.Back, k.Quit}}
}
