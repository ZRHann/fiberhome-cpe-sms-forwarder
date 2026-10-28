package main

import (
	"bufio"
	"bytes"
	"context"
	"log"
	"os/exec"
	"strings"
	"time"
)

func startUbusListener(ctx context.Context, events chan<- struct{}) {
	path, err := exec.LookPath("ubus")
	if err != nil {
		log.Printf("ubus is unavailable; using file polling only")
		return
	}
	go func() {
		for ctx.Err() == nil {
			command := exec.CommandContext(ctx, path, "listen", "cpe_new_sms_notify")
			stdout, err := command.StdoutPipe()
			if err != nil {
				log.Printf("create ubus listener: %v", err)
				return
			}
			var stderr bytes.Buffer
			command.Stderr = &stderr
			if err := command.Start(); err != nil {
				log.Printf("start ubus listener: %v", err)
				if !sleepWithContext(ctx, 5*time.Second) {
					return
				}
				continue
			}
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				if !strings.Contains(scanner.Text(), `"cpe_new_sms_notify"`) {
					continue
				}
				select {
				case events <- struct{}{}:
				default:
				}
			}
			err = command.Wait()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				log.Printf("ubus listener stopped: %v: %s", err, strings.TrimSpace(stderr.String()))
			} else if err := scanner.Err(); err != nil {
				log.Printf("read ubus listener: %v", err)
			}
			if !sleepWithContext(ctx, 5*time.Second) {
				return
			}
		}
	}()
}

func sleepWithContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
