package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type smsMetadata struct {
	Filename   string
	ReceivedAt string
	Sender     string
	Recipient  string
	Schema     string
}

type smsMessage struct {
	Filename   string
	Sender     string
	ReceivedAt string
	Content    string
	Hash       string
}

func hashSMSFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func readSMSMessage(path, metadataDir string) (smsMessage, error) {
	filename := filepath.Base(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return smsMessage{}, fmt.Errorf("read %s: %w", filename, err)
	}
	metadata, err := findSMSMetadata(metadataDir, filename)
	if err != nil {
		return smsMessage{}, err
	}
	content, err := decodeSMSContent(data, metadata.Schema)
	if err != nil {
		return smsMessage{}, fmt.Errorf("decode %s: %w", filename, err)
	}
	sender, receivedAt := parseSMSFilename(filename)
	if metadata.Sender != "" {
		sender = metadata.Sender
	}
	if metadata.ReceivedAt != "" {
		receivedAt = metadata.ReceivedAt
	}
	digest := sha256.Sum256(data)
	return smsMessage{
		Filename:   filename,
		Sender:     sender,
		ReceivedAt: receivedAt,
		Content:    content,
		Hash:       hex.EncodeToString(digest[:]),
	}, nil
}

func findSMSMetadata(root, filename string) (smsMetadata, error) {
	if root == "" {
		return smsMetadata{}, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return smsMetadata{}, nil
		}
		return smsMetadata{}, fmt.Errorf("read SMS metadata directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name(), "value.conf"))
		if err != nil {
			continue
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		if metadataLine(lines, 1) != filename {
			continue
		}
		return smsMetadata{
			Filename:   filename,
			ReceivedAt: metadataLine(lines, 2),
			Sender:     metadataLine(lines, 4),
			Recipient:  metadataLine(lines, 5),
			Schema:     metadataLine(lines, 6),
		}, nil
	}
	return smsMetadata{}, nil
}

func metadataLine(lines []string, number int) string {
	if number < 1 || number > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[number-1])
}

func decodeSMSContent(data []byte, schema string) (string, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("empty message")
	}
	if schema == "" || schema == "8" {
		decoded, err := decodeUTF16Hex(text)
		if err == nil {
			return decoded, nil
		}
		if schema == "8" {
			return "", err
		}
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("message is neither UTF-16 hex nor UTF-8")
	}
	return string(data), nil
}

func decodeUTF16Hex(text string) (string, error) {
	compact := strings.Join(strings.Fields(text), "")
	data, err := hex.DecodeString(compact)
	if err != nil {
		return "", err
	}
	if len(data) == 0 || len(data)%2 != 0 {
		return "", fmt.Errorf("invalid UTF-16 byte length %d", len(data))
	}
	units := make([]uint16, len(data)/2)
	for index := range units {
		units[index] = binary.BigEndian.Uint16(data[index*2:])
	}
	runes := make([]rune, 0, len(units))
	for index := 0; index < len(units); index++ {
		unit := units[index]
		switch {
		case unit >= 0xd800 && unit <= 0xdbff:
			if index+1 >= len(units) || units[index+1] < 0xdc00 || units[index+1] > 0xdfff {
				return "", fmt.Errorf("unpaired UTF-16 high surrogate")
			}
			runes = append(runes, utf16.DecodeRune(rune(unit), rune(units[index+1])))
			index++
		case unit >= 0xdc00 && unit <= 0xdfff:
			return "", fmt.Errorf("unpaired UTF-16 low surrogate")
		default:
			runes = append(runes, rune(unit))
		}
	}
	return strings.TrimPrefix(string(runes), "\ufeff"), nil
}

func parseSMSFilename(filename string) (string, string) {
	parts := strings.Split(filename, "_")
	if len(parts) < 2 {
		return "", ""
	}
	sender := parts[1]
	timestamp := ""
	if len(parts) >= 4 {
		timestamp = parts[3]
	} else {
		timestamp = strings.TrimPrefix(parts[0], "sms")
	}
	return sender, formatSMSTimestamp(timestamp)
}

func formatSMSTimestamp(timestamp string) string {
	if len(timestamp) < 12 {
		return ""
	}
	for _, character := range timestamp[:12] {
		if character < '0' || character > '9' {
			return ""
		}
	}
	return fmt.Sprintf("20%s-%s-%s %s:%s:%s",
		timestamp[0:2], timestamp[2:4], timestamp[4:6],
		timestamp[6:8], timestamp[8:10], timestamp[10:12])
}

func (message smsMessage) NotificationText() string {
	sender := message.Sender
	if sender == "" {
		sender = "未知"
	}
	receivedAt := message.ReceivedAt
	if receivedAt == "" {
		receivedAt = "未知"
	}
	return fmt.Sprintf("【CPE SMS】新短信\n发件人：%s\n时间：%s\n\n%s", sender, receivedAt, message.Content)
}
