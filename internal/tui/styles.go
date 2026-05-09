package tui

import "github.com/charmbracelet/lipgloss"

var (
	colBorder    = lipgloss.Color("240")
	colMuted     = lipgloss.Color("245")
	colAccent    = lipgloss.Color("212")
	colMention   = lipgloss.Color("214")
	colSelf      = lipgloss.Color("117")
	colError     = lipgloss.Color("196")
	colSuccess   = lipgloss.Color("82")
	colDimText   = lipgloss.Color("250")
	colOnline    = lipgloss.Color("82")
	colOffline   = lipgloss.Color("240")

	stBorder = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(colBorder)
	stTitle  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	stMuted  = lipgloss.NewStyle().Foreground(colMuted)
	stAccent = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stMention = lipgloss.NewStyle().Foreground(colMention).Bold(true)
	stError  = lipgloss.NewStyle().Foreground(colError)
	stOk     = lipgloss.NewStyle().Foreground(colSuccess)
	stTimestamp = lipgloss.NewStyle().Foreground(colMuted)
	stHandle = lipgloss.NewStyle().Foreground(colSelf).Bold(true)
	stSelected = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stOnlineDot = lipgloss.NewStyle().Foreground(colOnline)
	stOfflineDot = lipgloss.NewStyle().Foreground(colOffline)
	stStatusBar = lipgloss.NewStyle().Foreground(colDimText).Background(lipgloss.Color("236")).Padding(0, 1)
)
