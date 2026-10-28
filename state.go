package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type forwarderState struct {
	Initialized bool              `json:"initialized"`
	Seen        map[string]string `json:"seen"`
}

func loadForwarderState(path string) (forwarderState, error) {
	state := forwarderState{Seen: make(map[string]string)}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, fmt.Errorf("read state: %w", err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("decode state: %w", err)
	}
	if state.Seen == nil {
		state.Seen = make(map[string]string)
	}
	return state, nil
}

func saveForwarderState(path string, state forwarderState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".state-*")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	temporary := file.Name()
	defer func() {
		file.Close()
		os.Remove(temporary)
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("protect state: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}
