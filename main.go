package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const defaultMetadataDir = "/tmp/InternetGatewayDevice/X_FH_MobileNetwork/SMS_Recv/SMS_RecvMsg"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "运行失败:", err)
		os.Exit(1)
	}
}

func run() error {
	webhook := flag.String("webhook", os.Getenv("FEISHU_WEBHOOK"), "Feishu custom bot webhook URL")
	secret := flag.String("secret", os.Getenv("FEISHU_SECRET"), "Feishu signing secret")
	caFile := flag.String("ca-file", os.Getenv("FEISHU_CA_FILE"), "additional CA certificate bundle")
	timeout := flag.Duration("timeout", 15*time.Second, "request timeout")
	timestampOffset := flag.Duration("timestamp-offset", -8*time.Hour, "clock offset for Feishu signatures; use 0 for a correctly synced clock")
	text := flag.String("text", "", "send one text message instead of running the forwarder")
	smsDir := flag.String("sms-dir", "/data/fhconf/sms", "directory containing received SMS files")
	metadataDir := flag.String("metadata-dir", defaultMetadataDir, "directory containing SMS value.conf metadata")
	stateFile := flag.String("state-file", "/data/fhconf/sms-forwarder/state.json", "persistent deduplication state")
	logFile := flag.String("log-file", "", "append logs to this file instead of stderr")
	pollInterval := flag.Duration("poll-interval", time.Minute, "periodic SMS reconciliation interval")
	retryInterval := flag.Duration("retry-interval", 15*time.Second, "delay after a polling or forwarding failure")
	settleDelay := flag.Duration("settle-delay", 500*time.Millisecond, "required stable time before reading a new SMS file")
	ubusEnabled := flag.Bool("ubus", true, "listen for cpe_new_sms_notify events")
	once := flag.Bool("once", false, "perform one scan and exit")
	flag.Parse()
	if *logFile != "" {
		if err := configureLogging(*logFile); err != nil {
			return err
		}
	}

	if *webhook == "" {
		flag.Usage()
		return errors.New("FEISHU_WEBHOOK or -webhook is required")
	}
	if *pollInterval <= 0 || *retryInterval <= 0 || *settleDelay < 0 {
		return errors.New("poll/retry intervals must be positive and settle delay cannot be negative")
	}
	if *stateFile == "" {
		return errors.New("state file is required")
	}

	client, err := newFeishuHTTPClient(*caFile, *timeout)
	if err != nil {
		return err
	}
	sender := &FeishuSender{
		WebhookURL:      *webhook,
		Secret:          *secret,
		Client:          client,
		TimestampOffset: *timestampOffset,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	message := *text
	if message == "" {
		message = strings.Join(flag.Args(), " ")
	}
	if message != "" {
		if err := sender.SendText(ctx, message); err != nil {
			return err
		}
		fmt.Println("飞书消息发送成功")
		return nil
	}

	forwarder := &Forwarder{
		SMSDir:        *smsDir,
		MetadataDir:   *metadataDir,
		StateFile:     *stateFile,
		Sender:        sender,
		PollInterval:  *pollInterval,
		RetryInterval: *retryInterval,
		SettleDelay:   *settleDelay,
	}
	if *once {
		return forwarder.Poll(ctx)
	}
	events := make(chan struct{}, 1)
	if *ubusEnabled {
		startUbusListener(ctx, events)
		forwarder.Events = events
	}
	log.Printf("SMS forwarder started; watching %s", *smsDir)
	if err := forwarder.Run(ctx); err != nil {
		return err
	}
	log.Printf("SMS forwarder stopped")
	return nil
}
