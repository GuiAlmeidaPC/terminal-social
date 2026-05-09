package ssh

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	lm "github.com/charmbracelet/wish/logging"

	"github.com/tsocial/tsocial/internal/auth"
	"github.com/tsocial/tsocial/internal/config"
	"github.com/tsocial/tsocial/internal/hub"
	"github.com/tsocial/tsocial/internal/rate"
	"github.com/tsocial/tsocial/internal/store"
	"github.com/tsocial/tsocial/internal/tui"
)

type Server struct {
	cfg      *config.Config
	store    *store.Store
	hub      *hub.Hub
	log      *slog.Logger
	sessions atomic.Int64
	connRate *rate.Limiter
	msgRate  *rate.Limiter
}

func New(cfg *config.Config, st *store.Store, h *hub.Hub, log *slog.Logger) *Server {
	return &Server{
		cfg:      cfg,
		store:    st,
		hub:      h,
		log:      log,
		connRate: rate.NewLimiter(20, time.Minute),
		msgRate:  rate.NewLimiter(cfg.RateMsgPer10s, 10*time.Second),
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.cfg.HostKey), 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}

	srv, err := wish.NewServer(
		wish.WithAddress(s.cfg.Listen),
		wish.WithHostKeyPath(s.cfg.HostKey),
		wish.WithPublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
			ip := remoteIP(ctx.RemoteAddr())
			if !s.connRate.Allow(ip) {
				return false
			}
			ctx.SetValue("pubkey", key)
			ctx.SetValue("fingerprint", auth.Fingerprint(key))
			return true
		}),
		wish.WithMiddleware(
			bm.Middleware(s.teaHandler),
			lm.Middleware(),
		),
	)
	if err != nil {
		return err
	}

	// Reject exec, subsystem, and port-forward requests.
	srv.ChannelHandlers = map[string]ssh.ChannelHandler{
		"session": ssh.DefaultSessionHandler,
	}
	srv.SubsystemHandlers = map[string]ssh.SubsystemHandler{}

	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	s.log.Info("ssh listening", "addr", s.cfg.Listen)
	return srv.ListenAndServe()
}

func (s *Server) teaHandler(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
	if cur := s.sessions.Add(1); cur > int64(s.cfg.MaxSessions) {
		s.sessions.Add(-1)
		wish.Println(sess, "server is at capacity, please try again later")
		_ = sess.Exit(1)
		return nil, nil
	}

	pty, _, active := sess.Pty()
	if !active {
		s.sessions.Add(-1)
		wish.Println(sess, "this server requires a PTY (use a terminal SSH client)")
		_ = sess.Exit(1)
		return nil, nil
	}

	// Reject exec — only interactive shell allowed.
	if cmd := sess.Command(); len(cmd) > 0 {
		s.sessions.Add(-1)
		wish.Println(sess, "exec not supported; this server is interactive-only")
		_ = sess.Exit(1)
		return nil, nil
	}

	fp, _ := sess.Context().Value("fingerprint").(string)
	pkey, _ := sess.Context().Value("pubkey").(ssh.PublicKey)
	if fp == "" || pkey == nil {
		s.sessions.Add(-1)
		_ = sess.Exit(1)
		return nil, nil
	}

	ctx := context.Background()
	user, err := s.store.UserByFingerprint(ctx, fp)
	known := err == nil && user != nil
	if known {
		s.store.TouchLastSeen(ctx, user.ID)
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			s.sessions.Add(-1)
		})
	}

	// per-user message rate is keyed on the user-id once known; for a
	// not-yet-registered fingerprint we use the fingerprint as a stable key.
	rateKey := fp
	if known {
		rateKey = "u:" + strconv.FormatInt(user.ID, 10)
	}
	msgAllow := func() bool { return s.msgRate.Allow(rateKey) }

	deps := tui.Deps{
		Store:        s.store,
		Hub:          s.hub,
		Log:          s.log,
		Fingerprint:  fp,
		PublicKey:    auth.AuthorizedKey(pkey),
		ShortFP:      auth.ShortFingerprint(pkey),
		DefaultRoom:  s.cfg.DefaultRoom,
		RateMsg:      msgAllow,
		Width:        pty.Window.Width,
		Height:       pty.Window.Height,
		Done:         sess.Context().Done(),
		OnDisconnect: cleanup,
	}

	var m tea.Model
	if known {
		if user.IsSuspended {
			m = tui.NewSuspendedModel(user, deps)
		} else {
			m = tui.NewAppModel(deps, user)
		}
	} else {
		m = tui.NewRegisterModel(deps)
	}

	return m, []tea.ProgramOption{tea.WithAltScreen()}
}

func remoteIP(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		return host
	}
	return addr.String()
}
