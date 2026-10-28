package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type textSender interface {
	SendText(context.Context, string) error
}

type Forwarder struct {
	SMSDir        string
	MetadataDir   string
	StateFile     string
	Sender        textSender
	PollInterval  time.Duration
	RetryInterval time.Duration
	SettleDelay   time.Duration
	Events        <-chan struct{}

	state       forwarderState
	stateLoaded bool
}

type smsCandidate struct {
	name    string
	path    string
	modTime time.Time
}

func (f *Forwarder) Poll(ctx context.Context) error {
	if !f.stateLoaded {
		state, err := loadForwarderState(f.StateFile)
		if err != nil {
			return err
		}
		f.state = state
		f.stateLoaded = true
	}
	entries, err := os.ReadDir(f.SMSDir)
	if err != nil {
		return fmt.Errorf("read SMS directory: %w", err)
	}
	candidates := make([]smsCandidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "sms") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		candidates = append(candidates, smsCandidate{
			name:    entry.Name(),
			path:    filepath.Join(f.SMSDir, entry.Name()),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime.Equal(candidates[j].modTime) {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].modTime.Before(candidates[j].modTime)
	})

	if !f.state.Initialized {
		for _, candidate := range candidates {
			hash, err := hashSMSFile(candidate.path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return fmt.Errorf("hash %s: %w", candidate.name, err)
			}
			f.state.Seen[candidate.name] = hash
		}
		f.state.Initialized = true
		if err := saveForwarderState(f.StateFile, f.state); err != nil {
			return err
		}
		log.Printf("initialized SMS baseline with %d existing files", len(candidates))
		return nil
	}

	var failures []error
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		hash, err := hashSMSFile(candidate.path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			failures = append(failures, fmt.Errorf("hash %s: %w", candidate.name, err))
			continue
		}
		if f.state.Seen[candidate.name] == hash {
			continue
		}
		if err := waitForStableFile(ctx, candidate.path, f.SettleDelay); err != nil {
			failures = append(failures, err)
			continue
		}
		message, err := readSMSMessage(candidate.path, f.MetadataDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			failures = append(failures, err)
			continue
		}
		if f.state.Seen[candidate.name] == message.Hash {
			continue
		}
		if err := f.Sender.SendText(ctx, message.NotificationText()); err != nil {
			failures = append(failures, fmt.Errorf("forward %s: %w", candidate.name, err))
			continue
		}
		f.state.Seen[candidate.name] = message.Hash
		if err := saveForwarderState(f.StateFile, f.state); err != nil {
			failures = append(failures, err)
			continue
		}
		log.Printf("forwarded SMS %s from %s", message.Filename, message.Sender)
	}
	return errors.Join(failures...)
}

func waitForStableFile(ctx context.Context, path string, delay time.Duration) error {
	before, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", filepath.Base(path), err)
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("recheck %s: %w", filepath.Base(path), err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("SMS file %s is still being written", filepath.Base(path))
	}
	return nil
}

func (f *Forwarder) Run(ctx context.Context) error {
	events := f.Events
	for {
		err := f.Poll(ctx)
		if ctx.Err() != nil {
			return nil
		}
		delay := f.PollInterval
		if err != nil {
			log.Printf("SMS poll failed: %v", err)
			delay = f.RetryInterval
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		case _, ok := <-events:
			timer.Stop()
			if !ok {
				events = nil
			}
		}
	}
}
