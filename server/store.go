package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type eventStore interface {
	InsertPending(record outboxRecord) (bool, error)
	Get(eventID string) (*outboxRecord, error)
	Update(eventID string, update func(record *outboxRecord) error) error
	ListRecoverable() ([]string, error)
}

type kvEventStore struct {
	api mmAPI
}

func newKVEventStore(api mmAPI) *kvEventStore {
	return &kvEventStore{api: api}
}

func (s *kvEventStore) InsertPending(record outboxRecord) (bool, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return false, err
	}
	ok, appErr := s.api.KVCompareAndSet(outboxKey(record.Event.EventID), nil, data)
	if appErr != nil {
		return false, fmt.Errorf("KVCompareAndSet outbox: %w", appErr)
	}
	if !ok {
		return false, nil
	}
	if err := setKVJSON(s.api, outboxIndexKey(record.Event.EventID), map[string]string{"status": record.Status}); err != nil {
		return false, err
	}
	return true, nil
}

func (s *kvEventStore) Get(eventID string) (*outboxRecord, error) {
	raw, appErr := s.api.KVGet(outboxKey(eventID))
	if appErr != nil {
		return nil, fmt.Errorf("KVGet outbox: %w", appErr)
	}
	if raw == nil {
		return nil, nil
	}
	var record outboxRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *kvEventStore) Update(eventID string, update func(record *outboxRecord) error) error {
	for {
		raw, appErr := s.api.KVGet(outboxKey(eventID))
		if appErr != nil {
			return fmt.Errorf("KVGet outbox update: %w", appErr)
		}
		if raw == nil {
			return nil
		}
		var record outboxRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		if err := update(&record); err != nil {
			return err
		}
		next, err := json.Marshal(&record)
		if err != nil {
			return err
		}
		ok, appErr := s.api.KVCompareAndSet(outboxKey(eventID), raw, next)
		if appErr != nil {
			return fmt.Errorf("KVCompareAndSet outbox update: %w", appErr)
		}
		if !ok {
			continue
		}
		return setKVJSON(s.api, outboxIndexKey(eventID), map[string]string{"status": record.Status})
	}
}

func (s *kvEventStore) ListRecoverable() ([]string, error) {
	var keys []string
	page := 0
	for {
		pageKeys, appErr := s.api.KVList(page, 200)
		if appErr != nil {
			return nil, fmt.Errorf("KVList: %w", appErr)
		}
		if len(pageKeys) == 0 {
			break
		}
		for _, key := range pageKeys {
			if !strings.HasPrefix(key, outboxPrefix) {
				continue
			}
			eventID := strings.TrimPrefix(key, outboxPrefix)
			record, err := s.Get(eventID)
			if err != nil || record == nil {
				continue
			}
			if record.Status == eventStatusPending || record.Status == eventStatusProcessing {
				keys = append(keys, eventID)
			}
		}
		page++
	}
	sort.Strings(keys)
	return keys, nil
}

func outboxKey(eventID string) string {
	return outboxPrefix + eventID
}

func outboxIndexKey(eventID string) string {
	return outboxIndexPrefix + eventID
}

func setKVJSON(api mmAPI, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if appErr := api.KVSet(key, raw); appErr != nil {
		return fmt.Errorf("KVSet %s: %w", key, appErr)
	}
	return nil
}
