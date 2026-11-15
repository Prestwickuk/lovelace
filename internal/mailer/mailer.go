package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tirbach/tir-bach/internal/domain"
)

// Message represents an outbound email ready for dispatch.
type Message struct {
	From     string
	To       string
	Subject  string
	BodyText string
	BodyHTML string
}

// ValidateMessage guards against SMTP header injection and invalid addresses.
func ValidateMessage(msg Message) (Message, error) {
	fromClean, err := domain.SanitizeEmailHeaderValue(msg.From)
	if err != nil || fromClean == "" {
		return msg, Permanent(fmt.Errorf("invalid sender address: %w", domain.ErrValidation))
	}
	toClean, err := domain.ValidateEmailRecipient(msg.To)
	if err != nil {
		return msg, Permanent(err)
	}
	subjClean, err := domain.SanitizeEmailHeaderValue(msg.Subject)
	if err != nil || subjClean == "" {
		return msg, Permanent(fmt.Errorf("invalid email subject: %w", domain.ErrValidation))
	}
	msg.From = fromClean
	msg.To = toClean
	msg.Subject = subjClean
	return msg, nil
}

// DefaultSMTPTimeout is the default bounded external network/protocol timeout for SMTPSender.
const DefaultSMTPTimeout = 30 * time.Second

const defaultSMTPTimeout = DefaultSMTPTimeout

// Sender transmits prepared email messages (SMTP or structured log sink in dev/test).
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// SMTPSender dispatches email via net/smtp when TIRBACH_SMTP_ADDR is configured.
// Send honors ctx cancellation/deadlines and bounds blocking network I/O via Timeout.
type SMTPSender struct {
	Addr      string
	Username  string
	Password  string
	Timeout   time.Duration
	TLSConfig *tls.Config
}

func wrapPreSubmitErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil || errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		cause := ctx.Err()
		if cause != nil {
			return TerminatedBeforeSend(errors.Join(cause, err))
		}
		return TerminatedBeforeSend(err)
	}
	return err
}

func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if ctx == nil {
		ctx = context.Background()
	}
	clean, err := ValidateMessage(msg)
	if err != nil {
		return err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = defaultSMTPTimeout
	}
	if parentDeadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(parentDeadline)
		if remaining <= 0 {
			return TerminatedBeforeSend(context.DeadlineExceeded)
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	sendCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := sendCtx.Err(); err != nil {
		return TerminatedBeforeSend(err)
	}

	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		host = s.Addr
	}
	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, host)
	}
	var bodyBuilder strings.Builder
	bodyBuilder.WriteString("From: " + clean.From + "\r\n")
	bodyBuilder.WriteString("To: " + clean.To + "\r\n")
	bodyBuilder.WriteString("Subject: " + clean.Subject + "\r\n")
	bodyBuilder.WriteString("MIME-Version: 1.0\r\n")
	if strings.TrimSpace(clean.BodyHTML) != "" {
		mw := multipart.NewWriter(&bodyBuilder)
		bodyBuilder.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q\r\n\r\n", mw.Boundary()))
		tp, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=UTF-8"}})
		if err != nil {
			return err
		}
		_, _ = tp.Write([]byte(clean.BodyText))
		hp, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/html; charset=UTF-8"}})
		if err != nil {
			return err
		}
		_, _ = hp.Write([]byte(clean.BodyHTML))
		_ = mw.Close()
	} else {
		bodyBuilder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		bodyBuilder.WriteString(clean.BodyText)
	}

	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(sendCtx, "tcp", s.Addr)
	if err != nil {
		return wrapPreSubmitErr(sendCtx, err)
	}
	if deadline, ok := sendCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	stopWatch := make(chan struct{})
	var watchWG sync.WaitGroup
	watchWG.Add(1)
	go func() {
		defer watchWG.Done()
		select {
		case <-sendCtx.Done():
			_ = conn.SetDeadline(time.Now())
			_ = conn.Close()
		case <-stopWatch:
		}
	}()
	defer func() {
		close(stopWatch)
		watchWG.Wait()
		_ = conn.Close()
	}()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return wrapPreSubmitErr(sendCtx, err)
	}
	defer func() {
		_ = client.Close()
	}()

	if ext, _ := client.Extension("STARTTLS"); ext {
		tlsCfg := s.TLSConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{ServerName: host}
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return wrapPreSubmitErr(sendCtx, err)
		}
	}
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(auth); err != nil {
				return wrapPreSubmitErr(sendCtx, err)
			}
		}
	}
	if err := client.Mail(clean.From); err != nil {
		return wrapPreSubmitErr(sendCtx, err)
	}
	if err := client.Rcpt(clean.To); err != nil {
		return wrapPreSubmitErr(sendCtx, err)
	}

	wc, err := client.Data()
	if err != nil {
		return wrapPreSubmitErr(sendCtx, err)
	}

	if _, err := wc.Write([]byte(bodyBuilder.String())); err != nil {
		return Uncertain(err)
	}
	if err := wc.Close(); err != nil {
		// Definite SMTP negative reply when transport stayed alive is not uncertain.
		var tpErr *textproto.Error
		if errors.As(err, &tpErr) && sendCtx.Err() == nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			return err
		}
		return Uncertain(err)
	}

	_ = client.Quit()
	return nil
}

// LogSender is a development-only no-op sink that logs emails locally without
// dispatching them via a genuine delivery provider. It must never be wired
// automatically when SMTP is unconfigured.
type LogSender struct{}

func (LogSender) Send(ctx context.Context, msg Message) error {
	if ctx != nil && ctx.Err() != nil {
		return TerminatedBeforeSend(ctx.Err())
	}
	clean, err := ValidateMessage(msg)
	if err != nil {
		return err
	}
	slog.Info("transactional email logged only (dev log sink, not genuinely delivered)",
		"to", clean.To,
		"subject", clean.Subject,
	)
	return nil
}

// BuildBookingConfirmation renders a Tir Bach / property-branded confirmation & receipt email
// with subtle LOVELACE platform footer attribution.
func BuildBookingConfirmation(
	prop domain.Property,
	policy domain.PropertyBookingPolicy,
	res domain.Reservation,
	quote domain.BookingQuote,
	paidAmountPence int64,
) (subject, bodyText, bodyHTML string) {
	subject = fmt.Sprintf("Booking Confirmation %s — %s", res.Reference, prop.Name)
	totalStr := domain.FormatPence(quote.TotalPence, quote.Currency)
	paidStr := domain.FormatPence(paidAmountPence, quote.Currency)
	balanceStr := domain.FormatPence(res.BalanceDuePence, quote.Currency)

	var balanceNote string
	if res.BalanceDuePence > 0 && quote.BalanceDueDate != "" {
		balanceNote = fmt.Sprintf("Remaining balance: %s (due by %s)", balanceStr, quote.BalanceDueDate)
	} else {
		balanceNote = "Remaining balance: " + balanceStr
	}

	bodyText = fmt.Sprintf(`Hello %s,

Thank you for booking your stay at %s.

Booking Reference: %s
Accommodation: %s
Arrival Date: %s (Check-in from %s)
Departure Date: %s (Check-out by %s)
Guests: %d adults, %d children, %d infants

Price Summary:
Total Booking Price: %s
Payment Received: %s
%s

Cancellation Policy:
%s

Contact Details:
Email: %s
Phone: %s

Warm regards,
%s

---
Booking system powered by LOVELACE`,
		res.GuestName, prop.Name, res.Reference, quote.UnitTypeName,
		domain.FormatDate(res.ArrivalDate), prop.CheckInTime,
		domain.FormatDate(res.DepartureDate), prop.CheckOutTime,
		res.Adults, res.Children, res.Infants,
		totalStr, paidStr, balanceNote,
		policy.CancellationPolicy, prop.ContactEmail, prop.ContactPhone, prop.Name,
	)

	bodyHTML = fmt.Sprintf(`<div style="font-family:system-ui,sans-serif;max-width:600px;margin:0 auto;color:#1e211e">
<h2>%s — Booking Confirmation</h2>
<p>Hello %s, thank you for booking with us.</p>
<p><strong>Booking Reference:</strong> %s<br>
<strong>Accommodation:</strong> %s<br>
<strong>Arrival:</strong> %s (from %s)<br>
<strong>Departure:</strong> %s (by %s)<br>
<strong>Party:</strong> %d adults, %d children, %d infants</p>
<h3>Price &amp; Payment Summary</h3>
<p><strong>Total:</strong> %s<br>
<strong>Paid:</strong> %s<br>
<strong>%s</strong></p>
<p><strong>Cancellation Policy:</strong> %s</p>
<p><strong>Contact:</strong> %s %s</p>
<hr>
<p style="font-size:12px;color:#555">Booking system powered by LOVELACE</p>
</div>`,
		html.EscapeString(prop.Name),
		html.EscapeString(res.GuestName),
		html.EscapeString(res.Reference),
		html.EscapeString(quote.UnitTypeName),
		html.EscapeString(domain.FormatDate(res.ArrivalDate)), html.EscapeString(prop.CheckInTime),
		html.EscapeString(domain.FormatDate(res.DepartureDate)), html.EscapeString(prop.CheckOutTime),
		res.Adults, res.Children, res.Infants,
		html.EscapeString(totalStr), html.EscapeString(paidStr), html.EscapeString(balanceNote),
		html.EscapeString(policy.CancellationPolicy),
		html.EscapeString(prop.ContactEmail), html.EscapeString(prop.ContactPhone),
	)

	return subject, bodyText, bodyHTML
}
