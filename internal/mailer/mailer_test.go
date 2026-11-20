package mailer

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPSender_PreDataTimeoutReturnsTerminatedBeforeSend(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	stopServer := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("220 localhost ESMTP\r\n"))
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			upper := strings.ToUpper(line)
			if strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO") {
				_, _ = conn.Write([]byte("250-localhost\r\n250 OK\r\n"))
			} else if strings.HasPrefix(upper, "MAIL FROM:") {
				// Stall before replying 250 / before DATA ever occurs.
				<-stopServer
				return
			}
		}
	}()

	s := &SMTPSender{
		Addr:    ln.Addr().String(),
		Timeout: 60 * time.Millisecond,
	}
	err = s.Send(context.Background(), Message{
		From:     "bookings@example.com",
		To:       "guest@example.test",
		Subject:  "Pre-DATA hang test",
		BodyText: "Hello",
	})
	_ = ln.Close()
	close(stopServer)
	<-doneCh
	if err == nil {
		t.Fatal("expected error when MTA stalls before DATA")
	}
	if !IsTerminatedBeforeSend(err) {
		t.Fatalf("expected IsTerminatedBeforeSend=true, got err=%v", err)
	}
	if IsUncertain(err) {
		t.Fatalf("did not expect IsUncertain=true for pre-DATA abort, got %v", err)
	}
}

func TestSMTPSender_PostDataTimeoutReturnsUncertain(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	gotDot := make(chan struct{})
	stopServer := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("220 localhost ESMTP\r\n"))
		r := bufio.NewReader(conn)
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					close(gotDot)
					// Stall indefinitely after final dot so client cannot tell if 250 OK happened.
					<-stopServer
					return
				}
				continue
			}
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
				_, _ = conn.Write([]byte("250-localhost\r\n250 OK\r\n"))
			case strings.HasPrefix(upper, "MAIL FROM:") || strings.HasPrefix(upper, "RCPT TO:"):
				_, _ = conn.Write([]byte("250 OK\r\n"))
			case strings.HasPrefix(upper, "DATA"):
				inData = true
				_, _ = conn.Write([]byte("354 End data with <CR><LF>.<CR><LF>\r\n"))
			}
		}
	}()

	s := &SMTPSender{
		Addr:    ln.Addr().String(),
		Timeout: 100 * time.Millisecond,
	}
	err = s.Send(context.Background(), Message{
		From:     "bookings@example.com",
		To:       "guest@example.test",
		Subject:  "Post-DATA hang test",
		BodyText: "Hello",
	})
	_ = ln.Close()
	close(stopServer)
	<-doneCh
	select {
	case <-gotDot:
	default:
		t.Fatalf("expected MTA to receive DATA dot before timeout (send err=%v)", err)
	}
	if err == nil {
		t.Fatal("expected error when MTA stalls after DATA")
	}
	if !IsUncertain(err) {
		t.Fatalf("expected IsUncertain=true after DATA submission timeout, got %v", err)
	}
	if IsTerminatedBeforeSend(err) {
		t.Fatalf("did not expect IsTerminatedBeforeSend=true after DATA dot was transmitted")
	}
}

func TestSMTPSender_ParentDeadlineCapsOversizedTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	stopServer := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("220 localhost ESMTP\r\n"))
		<-stopServer
	}()

	s := &SMTPSender{
		Addr:    ln.Addr().String(),
		Timeout: 5 * time.Minute, // oversized vs parent context deadline
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = s.Send(ctx, Message{
		From:     "bookings@example.com",
		To:       "guest@example.test",
		Subject:  "Deadline cap test",
		BodyText: "Hello",
	})
	elapsed := time.Since(start)
	_ = ln.Close()
	close(stopServer)
	<-doneCh
	if err == nil || !IsTerminatedBeforeSend(err) {
		t.Fatalf("expected TerminatedBeforeSend due to capped ctx deadline, got err=%v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("expected parent ctx deadline to bound oversized SMTPSender.Timeout promptly, took %s", elapsed)
	}
}
