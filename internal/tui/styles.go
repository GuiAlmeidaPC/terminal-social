package tui

import "github.com/charmbracelet/lipgloss"

var (
	colBorder    = lipgloss.Color("240")
	colMuted     = lipgloss.Color("245")
	colAccent    = lipgloss.Color("212")
	colActive    = lipgloss.Color("51") // cyan — active pane border
	colMention   = lipgloss.Color("214")
	colSelf      = lipgloss.Color("117")
	colError     = lipgloss.Color("196")
	colSuccess   = lipgloss.Color("82")
	colDimText   = lipgloss.Color("250")
	colOnline    = lipgloss.Color("82")
	colOffline   = lipgloss.Color("240")

	stBorder       = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(colBorder)
	stBorderActive = lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(colActive)
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
	stTitleBar  = lipgloss.NewStyle().Bold(true).Foreground(colActive).Background(lipgloss.Color("236")).Padding(0, 1).Align(lipgloss.Center)
)

// paneBorder returns the border style to use for a pane based on whether it
// currently has focus. Active panes get a thick accent-colored border;
// inactive panes get a thin muted border.
func paneBorder(active bool) lipgloss.Style {
	if active {
		return stBorderActive
	}
	return stBorder
}
