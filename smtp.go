package main

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

func sendStatusEmail(cfg SMTPConfig, result ProbeResult, state TargetState) error {
	label := "不可用"
	if state.Status == "up" {
		label = "可用"
	}
	subject := fmt.Sprintf("[CPA 模型监控] %s：%s", result.Name, label)
	body := fmt.Sprintf("监测项：%s\n模型：%s\n状态：%s\n检测时间：%s\n耗时：%d ms\nHTTP：%d", result.Name, result.Model, label, result.CheckedAt.Local().Format("2006-01-02 15:04:05 MST"), result.LatencyMS, result.HTTPStatus)
	if result.ErrorCode != "" {
		body += fmt.Sprintf("\n错误类型：%s\n错误：%s", result.ErrorCode, result.Error)
	}
	return sendEmail(cfg, subject, body)
}

func sendEmail(cfg SMTPConfig, subject, body string) error {
	if !cfg.Enabled {
		return errors.New("SMTP is disabled")
	}
	subject = strings.ReplaceAll(strings.ReplaceAll(subject, "\r", " "), "\n", " ")
	from := strings.ReplaceAll(strings.ReplaceAll(cfg.From, "\r", ""), "\n", "")
	to := make([]string, 0, len(cfg.To))
	for _, v := range cfg.To {
		v = strings.ReplaceAll(strings.ReplaceAll(v, "\r", ""), "\n", "")
		if v != "" {
			to = append(to, v)
		}
	}
	if len(to) == 0 {
		return errors.New("SMTP has no recipients")
	}
	msg := strings.Join([]string{"From: " + from, "To: " + strings.Join(to, ", "), "Subject: " + mime.QEncoding.Encode("UTF-8", subject), "MIME-Version: 1.0", "Content-Type: text/plain; charset=UTF-8", "Content-Transfer-Encoding: 8bit", "", body, ""}, "\r\n")
	return smtpSend(cfg, to, strings.NewReader(msg))
}

func smtpSend(cfg SMTPConfig, to []string, message io.Reader) error {
	address := smtpAddress(cfg)
	dialer := net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	var err error
	if cfg.TLSMode == "implicit" {
		conn, err = tls.DialWithDialer(&dialer, "tcp", address, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return fmt.Errorf("connect SMTP: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("start SMTP: %w", err)
	}
	defer client.Close()
	if cfg.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}
	if cfg.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("SMTP server does not advertise AUTH")
		}
		if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}
	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("SMTP RCPT TO: %w", err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	buffered := bufio.NewWriter(writer)
	if _, err = io.Copy(buffered, message); err == nil {
		err = buffered.Flush()
	}
	closeErr := writer.Close()
	if err != nil {
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("finish SMTP message: %w", closeErr)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("SMTP QUIT: %w", err)
	}
	return nil
}
