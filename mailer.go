package main

import (
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

func validateSMTP(config SMTPConfig) error {
	if strings.TrimSpace(config.Host) == "" {
		return fmt.Errorf("SMTP 主机不能为空")
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("SMTP 端口无效")
	}
	switch config.Security {
	case "starttls", "ssl", "none":
	default:
		return fmt.Errorf("SMTP 加密方式无效")
	}
	if len(normalizeRecipients(config.Recipients)) == 0 {
		return fmt.Errorf("至少需要一个收件人")
	}
	if _, err := parseAddress(config.From, config.Username); err != nil {
		return err
	}
	if config.Security == "none" && config.Username != "" {
		return fmt.Errorf("使用账号密码时请选择 STARTTLS 或 SSL")
	}
	return nil
}

func sendEmail(config SMTPConfig, subject, body string) error {
	if err := validateSMTP(config); err != nil {
		return err
	}
	from, err := parseAddress(config.From, config.Username)
	if err != nil {
		return err
	}
	recipients := normalizeRecipients(config.Recipients)
	parsedRecipients := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		address, err := mail.ParseAddress(recipient)
		if err != nil {
			return fmt.Errorf("收件人 %q 无效", recipient)
		}
		parsedRecipients = append(parsedRecipients, address.Address)
	}

	hostPort := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	tlsConfig := &tls.Config{
		ServerName: config.Host,
		MinVersion: tls.VersionTLS12,
	}

	var client *smtp.Client
	if config.Security == "ssl" {
		connection, err := tls.DialWithDialer(
			&net.Dialer{Timeout: 15 * time.Second},
			"tcp",
			hostPort,
			tlsConfig,
		)
		if err != nil {
			return fmt.Errorf("建立 SSL 连接失败: %w", err)
		}
		client, err = smtp.NewClient(connection, config.Host)
		if err != nil {
			connection.Close()
			return fmt.Errorf("初始化 SMTP 失败: %w", err)
		}
	} else {
		connection, err := net.DialTimeout("tcp", hostPort, 15*time.Second)
		if err != nil {
			return fmt.Errorf("连接 SMTP 主机失败: %w", err)
		}
		client, err = smtp.NewClient(connection, config.Host)
		if err != nil {
			connection.Close()
			return fmt.Errorf("初始化 SMTP 失败: %w", err)
		}
		if config.Security == "starttls" {
			if ok, _ := client.Extension("STARTTLS"); !ok {
				client.Close()
				return fmt.Errorf("SMTP 主机不支持 STARTTLS")
			}
			if err := client.StartTLS(tlsConfig); err != nil {
				client.Close()
				return fmt.Errorf("启动 STARTTLS 失败: %w", err)
			}
		}
	}
	defer client.Close()

	if config.Username != "" {
		auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 登录失败: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("设置发件人失败: %w", err)
	}
	for _, recipient := range parsedRecipients {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("设置收件人失败: %w", err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("开始写入邮件失败: %w", err)
	}
	message := buildMIMEMessage(from, parsedRecipients, subject, body)
	if _, err := writer.Write(message); err != nil {
		writer.Close()
		return fmt.Errorf("写入邮件失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("提交邮件失败: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("SMTP 会话结束异常: %w", err)
	}
	return nil
}

func parseAddress(from, username string) (*mail.Address, error) {
	value := strings.TrimSpace(from)
	if value == "" {
		value = strings.TrimSpace(username)
	}
	address, err := mail.ParseAddress(value)
	if err != nil {
		return nil, fmt.Errorf("发件人地址无效")
	}
	return address, nil
}

func normalizeRecipients(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		for _, item := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r'
		}) {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			key := strings.ToLower(item)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}

func buildMIMEMessage(from *mail.Address, recipients []string, subject, body string) []byte {
	subject = sanitizeHeader(subject)
	fromHeader := from.String()
	if from.Name == "" {
		fromHeader = from.Address
	}
	headers := []string{
		"From: " + fromHeader,
		"To: " + strings.Join(recipients, ", "),
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body + "\r\n")
}

func sanitizeHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}
