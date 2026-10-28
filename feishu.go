package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type FeishuSender struct {
	WebhookURL      string
	Secret          string
	Client          *http.Client
	TimestampOffset time.Duration
}

func (s *FeishuSender) SendText(ctx context.Context, text string) error {
	if s.WebhookURL == "" {
		return errors.New("webhook URL is required")
	}
	if text == "" {
		return errors.New("message text is required")
	}

	payload := map[string]any{
		"msg_type": "text",
		"content": map[string]string{
			"text": text,
		},
	}
	if s.Secret != "" {
		timestamp := time.Now().Add(s.TimestampOffset).Unix()
		payload["timestamp"] = timestamp
		payload["sign"] = feishuSignature(strconv.FormatInt(timestamp, 10), s.Secret)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("feishu HTTP %s: %s", resp.Status, responseSummary(responseBody))
	}

	var result struct {
		Code    *int   `json:"code"`
		Message string `json:"msg"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if result.Code == nil {
		return errors.New("feishu response is missing code")
	}
	if *result.Code != 0 {
		return fmt.Errorf("feishu error %d: %s", *result.Code, result.Message)
	}
	return nil
}

func feishuSignature(timestamp, secret string) string {
	mac := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func newFeishuHTTPClient(caFile string, timeout time.Duration) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		certificates, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		if !roots.AppendCertsFromPEM(certificates) {
			return nil, errors.New("CA file contains no certificates")
		}
		transport.TLSClientConfig = &tls.Config{
			RootCAs:    roots,
			MinVersion: tls.VersionTLS12,
		}
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

func responseSummary(body []byte) string {
	const limit = 2048
	text := strings.TrimSpace(string(body))
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}
