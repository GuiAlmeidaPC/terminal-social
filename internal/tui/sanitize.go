package tui

import "github.com/tsocial/tsocial/internal/domain"

// SanitizeUserText strips terminal control sequences and disallowed control
// chars from user-provided text before persisting/rendering. Allows \n and \t.
func SanitizeUserText(s string) string {
	return domain.SanitizeUserText(s)
}
