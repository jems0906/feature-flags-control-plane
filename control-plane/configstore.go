package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ConfigStore is a thread-safe key-value store backed by an in-memory map and
// a local JSON file.  It is a drop-in replacement for the Redis-backed store
// used in production; the file gives persistence across restarts without
// requiring any external services during development or CI.
type ConfigStore struct {
	mu        sync.RWMutex
	data      map[string]string
	filePath  string
	redisAddr string
}

// NewConfigStore creates (or reopens) a config store persisted to filePath.
// Pass an empty string to disable file persistence.
func NewConfigStore(filePath string) *ConfigStore {
	cs := &ConfigStore{
		data:     make(map[string]string),
		filePath: filePath,
	}
	if redisAddr := strings.TrimSpace(os.Getenv("REDIS_ADDR")); redisAddr != "" {
		cs.redisAddr = redisAddr
		if _, err := cs.redisCommand(context.Background(), "PING"); err != nil {
			cs.redisAddr = ""
			log.Printf("config store: redis unavailable at %s, falling back to file store: %v", redisAddr, err)
		} else {
			log.Printf("config store: using redis at %s", redisAddr)
			return cs
		}
	}
	cs.loadFromFile()
	return cs
}

func (cs *ConfigStore) Set(ctx context.Context, key, value string) error {
	if cs.redisAddr != "" {
		_, err := cs.redisCommand(ctx, "SET", key, value)
		return err
	}
	cs.mu.Lock()
	cs.data[key] = value
	cs.mu.Unlock()
	return cs.saveToFile()
}

func (cs *ConfigStore) Get(ctx context.Context, key string) (string, error) {
	if cs.redisAddr != "" {
		v, err := cs.redisCommand(ctx, "GET", key)
		if err != nil {
			return "", err
		}
		if v == nil {
			return "", fmt.Errorf("key not found: %s", key)
		}
		value, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("unexpected redis response type for key %s", key)
		}
		return value, nil
	}
	cs.mu.RLock()
	v, ok := cs.data[key]
	cs.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("key not found: %s", key)
	}
	return v, nil
}

func (cs *ConfigStore) Delete(ctx context.Context, key string) error {
	if cs.redisAddr != "" {
		_, err := cs.redisCommand(ctx, "DEL", key)
		return err
	}
	cs.mu.Lock()
	delete(cs.data, key)
	cs.mu.Unlock()
	return cs.saveToFile()
}

// GetAllByPrefix returns all entries whose key starts with prefix.
func (cs *ConfigStore) GetAllByPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	if cs.redisAddr != "" {
		out := make(map[string]string)
		keys, err := cs.redisScanKeys(ctx, prefix+"*")
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			valueResp, err := cs.redisCommand(ctx, "GET", key)
			if err != nil || valueResp == nil {
				continue
			}
			value, ok := valueResp.(string)
			if !ok {
				continue
			}
			out[key] = value
		}
		return out, nil
	}
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	out := make(map[string]string)
	for k, v := range cs.data {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out, nil
}

func (cs *ConfigStore) redisScanKeys(ctx context.Context, pattern string) ([]string, error) {
	allKeys := make([]string, 0)
	cursor := "0"
	for {
		resp, err := cs.redisCommand(ctx, "SCAN", cursor, "MATCH", pattern, "COUNT", "100")
		if err != nil {
			return nil, err
		}
		nextCursor, keys, err := redisScanResponse(resp)
		if err != nil {
			return nil, err
		}
		allKeys = append(allKeys, keys...)
		if nextCursor == "0" {
			break
		}
		cursor = nextCursor
	}
	return allKeys, nil
}

func (cs *ConfigStore) loadFromFile() {
	if cs.filePath == "" {
		return
	}
	f, err := os.Open(cs.filePath)
	if err != nil {
		return // file doesn't exist yet – that's fine
	}
	defer f.Close()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := json.NewDecoder(f).Decode(&cs.data); err != nil {
		// corrupt file – start fresh
		cs.data = make(map[string]string)
	}
}

func (cs *ConfigStore) saveToFile() error {
	if cs.filePath == "" {
		return nil
	}
	cs.mu.RLock()
	b, err := json.Marshal(cs.data)
	cs.mu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(cs.filePath, b, 0o600)
}

func (cs *ConfigStore) redisCommand(ctx context.Context, parts ...string) (any, error) {
	if cs.redisAddr == "" {
		return nil, errors.New("redis is not configured")
	}
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", cs.redisAddr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "*%d\r\n", len(parts))
	for _, part := range parts {
		fmt.Fprintf(&buf, "$%d\r\n%s\r\n", len(part), part)
	}
	if _, err := conn.Write(buf.Bytes()); err != nil {
		return nil, err
	}
	return readRESP(bufio.NewReader(conn))
}

func readRESP(r *bufio.Reader) (any, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	line, err := readRESPLine(r)
	if err != nil {
		return nil, err
	}
	switch prefix {
	case '+':
		return line, nil
	case '-':
		return nil, errors.New(line)
	case ':':
		return strconv.Atoi(line)
	case '$':
		length, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if length == -1 {
			return nil, nil
		}
		payload := make([]byte, length+2)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}
		return string(payload[:length]), nil
	case '*':
		count, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if count == -1 {
			return nil, nil
		}
		items := make([]any, 0, count)
		for i := 0; i < count; i++ {
			item, err := readRESP(r)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported redis response prefix %q", prefix)
	}
}

func readRESPLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func redisStringSlice(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected redis array type %T", v)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected redis array member %T", item)
		}
		out = append(out, text)
	}
	return out, nil
}

func redisScanResponse(v any) (string, []string, error) {
	parts, ok := v.([]any)
	if !ok || len(parts) != 2 {
		return "", nil, fmt.Errorf("unexpected redis SCAN response type %T", v)
	}
	nextCursor, ok := parts[0].(string)
	if !ok {
		return "", nil, fmt.Errorf("unexpected redis SCAN cursor type %T", parts[0])
	}
	keys, err := redisStringSlice(parts[1])
	if err != nil {
		return "", nil, err
	}
	return nextCursor, keys, nil
}
