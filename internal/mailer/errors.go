package mailer

import (
	"errors"
	"fmt"
	"net/textproto"
	"strings"
)

var (
	// ErrProviderUnconfigured indicates that no genuine email transport (such as
	// SMTP) is configured in this environment. Outbox messages must remain
	// PENDING until a real provider is configured.
	ErrProviderUnconfigured = errors.New("email delivery provider is not configured")

	// ErrPermanent wraps a non-retryable delivery failure (e.g., invalid address
	// or SMTP 5xx permanent rejection).
	ErrPermanent = errors.New("permanent email delivery failure")

	// ErrSendUncertain indicates a transport interruption/timeout occurred after
	// external submission may already have been accepted by the remote MTA.
	ErrSendUncertain = errors.New("email dispatch outcome uncertain after submission")

	// ErrTerminatedBeforeSend indicates transport execution was aborted cleanly
	// before payload submission (DATA) occurred.
	ErrTerminatedBeforeSend = errors.New("email dispatch terminated before submission")
)

// Permanent wraps err so dispatchers mark the outbox item FAILED immediately
// instead of retrying futilely.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrPermanent, err)
}

// Uncertain wraps err to mark that SMTP DATA submission started and external
// delivery outcome is unknown.
func Uncertain(err error) error {
	if err == nil {
		return ErrSendUncertain
	}
	return fmt.Errorf("%w: %v", ErrSendUncertain, err)
}

// TerminatedBeforeSend wraps err to prove transport was aborted before external
// submission started.
func TerminatedBeforeSend(err error) error {
	if err == nil {
		return ErrTerminatedBeforeSend
	}
	return fmt.Errorf("%w: %v", ErrTerminatedBeforeSend, err)
}

// IsUncertain reports whether err represents an uncertain post-submission
// transport failure or context expiration.
func IsUncertain(err error) bool {
	return err != nil && errors.Is(err, ErrSendUncertain)
}

// IsTerminatedBeforeSend reports whether transport stopped cleanly prior to
// payload submission.
func IsTerminatedBeforeSend(err error) bool {
	return err != nil && errors.Is(err, ErrTerminatedBeforeSend)
}

// IsPermanent reports whether err represents a non-retryable email failure,
// either via ErrPermanent or an SMTP 5xx permanent negative completion reply.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPermanent) {
		return true
	}
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) && tpErr.Code >= 500 && tpErr.Code < 600 {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "invalid email") || strings.Contains(msg, "recipient address is empty") {
		return true
	}
	return false
}

// IsGenuineProvider returns true when sender represents an actual mail
// transport capable of real delivery (SMTPSender or a custom test/production
// Sender) and false for nil or the dev-only LogSender.
func IsGenuineProvider(sender Sender) bool {
	if sender == nil {
		return false
	}
	switch s := sender.(type) {
	case LogSender:
		return false
	case *LogSender:
		return false
	case *SMTPSender:
		return s != nil && strings.TrimSpace(s.Addr) != ""
	default:
		return true
	}
}

// ProviderKind returns a safe non-secret identifier for readiness reporting.
func ProviderKind(sender Sender) string {
	if sender == nil {
		return "none"
	}
	switch s := sender.(type) {
	case LogSender, *LogSender:
		return "log_sink_only"
	case *SMTPSender:
		if s == nil || strings.TrimSpace(s.Addr) == "" {
			return "none"
		}
		return "smtp"
	default:
		return "custom"
	}
}
