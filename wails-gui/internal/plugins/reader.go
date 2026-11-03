package plugins

import "context"

// Reader exposes existing stored values without a write method. Interpretation
// belongs to each builtin so legacy defaults and encodings keep their meaning.
type Reader interface {
	Read(key string) (string, error)
}

type ReaderFunc func(string) (string, error)

func (read ReaderFunc) Read(key string) (string, error) {
	return read(key)
}

// UIStateStore is satisfied by storage.Store. Plugins only receive its read API.
type UIStateStore interface {
	GetUIState(context.Context, string) (string, error)
}

// NewStoredReader preserves the storage layer's exact values, missing-key
// behavior and errors. It never normalizes, migrates or rewrites a setting.
func NewStoredReader(ctx context.Context, store UIStateStore) Reader {
	return ReaderFunc(func(key string) (string, error) {
		return store.GetUIState(ctx, key)
	})
}

func ReadValues(reader Reader, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		value, err := reader.Read(key)
		if err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, nil
}
