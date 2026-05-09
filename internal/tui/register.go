package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tsocial/tsocial/internal/domain"
)

type RegisterModel struct {
	deps   Deps
	input  textinput.Model
	err    string
	width  int
	height int
}

func NewRegisterModel(d Deps) *RegisterModel {
	ti := textinput.New()
	ti.Placeholder = "your-handle"
	ti.CharLimit = domain.MaxHandleLen
	ti.Focus()
	ti.Prompt = "» "
	return &RegisterModel{deps: d, input: ti, width: d.Width, height: d.Height}
}

func (m *RegisterModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, registerDisconnectCmd(m.deps.Done))
}

func (m *RegisterModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cleanup()
			return m, tea.Quit
		case "enter":
			h := strings.ToLower(strings.TrimSpace(m.input.Value()))
			if !domain.ValidHandle(h) {
				m.err = "handle must be 3–20 chars, lowercase, [a-z0-9_-]"
				return m, nil
			}
			ctx := context.Background()
			if existing, _ := m.deps.Store.UserByHandle(ctx, h); existing != nil {
				m.err = "handle already taken"
				return m, nil
			}
			user, err := m.deps.Store.CreateUser(ctx, h, m.deps.Fingerprint, m.deps.PublicKey)
			if err != nil {
				m.err = "registration failed: " + err.Error()
				return m, nil
			}
			// auto-join default room (create if needed)
			room, err := m.deps.Store.RoomByName(ctx, m.deps.DefaultRoom)
			if err != nil {
				room, _ = m.deps.Store.CreateRoom(ctx, m.deps.DefaultRoom, false, user.ID)
			}
			if room != nil {
				_ = m.deps.Store.JoinRoom(ctx, room.ID, user.ID)
			}
			app := NewAppModel(m.deps, user)
			return app, app.Init()
		}
	case disconnectMsg:
		m.cleanup()
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *RegisterModel) cleanup() {
	if m.deps.OnDisconnect != nil {
		m.deps.OnDisconnect()
	}
}

func registerDisconnectCmd(done <-chan struct{}) tea.Cmd {
	if done == nil {
		return nil
	}
	return func() tea.Msg {
		<-done
		return disconnectMsg{}
	}
}

func (m *RegisterModel) View() string {
	body := lipgloss.JoinVertical(lipgloss.Left,
		stTitle.Render("Welcome to terminal-social"),
		"",
		stMuted.Render("By connecting you agree to be excellent to each other."),
		stMuted.Render("Your SSH key is your identity. Pick a handle to register."),
		stMuted.Render("Handles: 3–20 chars, lowercase letters/digits/_/-"),
		"",
		m.input.View(),
		"",
	)
	if m.err != "" {
		body = lipgloss.JoinVertical(lipgloss.Left, body, stError.Render(m.err))
	}
	body = lipgloss.JoinVertical(lipgloss.Left, body,
		stMuted.Render("press enter to register · ctrl+c to quit"),
		stMuted.Render("key fingerprint: "+m.deps.ShortFP),
	)
	w, h := m.width, m.height
	if w == 0 {
		w = 80
	}
	if h == 0 {
		h = 24
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center,
		stBorder.Padding(1, 2).Render(body))
}

// Suspended notice
type SuspendedModel struct {
	user *domain.User
	deps Deps
}

func NewSuspendedModel(u *domain.User, d Deps) *SuspendedModel {
	return &SuspendedModel{user: u, deps: d}
}
func (m *SuspendedModel) Init() tea.Cmd { return registerDisconnectCmd(m.deps.Done) }
func (m *SuspendedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		m.cleanup()
		return m, tea.Quit
	}
	if _, ok := msg.(disconnectMsg); ok {
		m.cleanup()
		return m, tea.Quit
	}
	return m, nil
}
func (m *SuspendedModel) View() string {
	return stError.Render("Your account is suspended.\nContact an admin.\n\nPress any key to disconnect.")
}

func (m *SuspendedModel) cleanup() {
	if m.deps.OnDisconnect != nil {
		m.deps.OnDisconnect()
	}
}
