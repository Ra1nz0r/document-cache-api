package cache

import (
	"fmt"
	"sync"
	"testing"
)

// putResponse сохраняет ответ с текущей версией кеша.
func putResponse(c *Cache, key Key, response Response) {
	_, generation, _ := c.Get(key)
	c.Set(key, response, generation)
}

func TestCacheSetAndGet(t *testing.T) {
	c := New(10, 1024)
	key := Key{
		UserID:     "user-1",
		DocumentID: "doc-1",
	}

	_, generation, hit := c.Get(key)
	if hit {
		t.Fatal("expected cache miss before saving")
	}

	want := Response{
		ContentType: "text/plain",
		Body:        "hello",
		NoSniff:     true,
	}
	c.Set(key, want, generation)

	got, _, hit := c.Get(key)
	if !hit {
		t.Fatal("expected cache hit after saving")
	}

	if got != want {
		t.Fatalf("unexpected response: got %+v, want %+v", got, want)
	}
}

func TestCacheSeparatesKeys(t *testing.T) {
	c := New(10, 1024)

	original := Key{
		UserID: "user-1",
		Query:  "limit=10",
	}
	putResponse(c, original, Response{Body: "list"})

	tests := []struct {
		name string
		key  Key
	}{
		{
			name: "another user",
			key: Key{
				UserID: "user-2",
				Query:  "limit=10",
			},
		},
		{
			name: "another query",
			key: Key{
				UserID: "user-1",
				Query:  "limit=20",
			},
		},
		{
			name: "document instead of list",
			key: Key{
				UserID:     "user-1",
				DocumentID: "doc-1",
				Query:      "limit=10",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, hit := c.Get(tt.key); hit {
				t.Fatal("different keys must not share a response")
			}
		})
	}
}

func TestCacheInvalidate(t *testing.T) {
	tests := []struct {
		name       string
		documentID string
	}{
		{
			name:       "lists only",
			documentID: "",
		},
		{
			name:       "lists and document",
			documentID: "doc-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(10, 1024)

			keys := []Key{
				{UserID: "user-1"},
				{UserID: "user-1", Query: "limit=1"},
				{UserID: "user-2"},
				{UserID: "user-1", DocumentID: "doc-1"},
				{UserID: "user-2", DocumentID: "doc-1"},
				{UserID: "user-1", DocumentID: "doc-2"},
			}

			for _, key := range keys {
				putResponse(c, key, Response{Body: "data"})
			}

			c.Invalidate(tt.documentID)

			for _, key := range keys {
				wantHit := key.DocumentID != "" &&
					key.DocumentID != tt.documentID

				_, _, hit := c.Get(key)
				if hit != wantHit {
					t.Errorf(
						"key %+v: hit = %v, want %v",
						key, hit, wantHit,
					)
				}
			}
		})
	}
}

// Воспроизводим порядок событий:
// GET начал чтение, произошла инвалидация, GET закончил чтение.
func TestCacheRejectsStaleResponse(t *testing.T) {
	c := New(10, 1024)
	key := Key{
		UserID:     "user-1",
		DocumentID: "doc-1",
	}

	_, oldGeneration, _ := c.Get(key)

	c.Invalidate("doc-1")

	c.Set(key, Response{Body: "stale"}, oldGeneration)

	if _, _, hit := c.Get(key); hit {
		t.Fatal("response from an old generation must not be cached")
	}

	// После инвалидации новые результаты должны сохраняться.
	_, currentGeneration, _ := c.Get(key)
	c.Set(key, Response{Body: "fresh"}, currentGeneration)

	// Старый запрос не должен затереть уже сохранённый новый ответ.
	c.Set(key, Response{Body: "stale"}, oldGeneration)

	got, _, hit := c.Get(key)
	if !hit || got.Body != "fresh" {
		t.Fatalf("expected fresh response, got %+v, hit=%v", got, hit)
	}
}

func TestCacheMaxEntries(t *testing.T) {
	c := New(2, 1024)

	keys := []Key{
		{UserID: "user-1", DocumentID: "doc-1"},
		{UserID: "user-1", DocumentID: "doc-2"},
		{UserID: "user-1", DocumentID: "doc-3"},
	}

	for _, key := range keys {
		putResponse(c, key, Response{Body: "data"})
	}

	// Новая запись должна сохраниться, одна из предыдущих вытесниться.
	if _, _, hit := c.Get(keys[2]); !hit {
		t.Fatal("expected the newly inserted response to be cached")
	}

	hits := 0
	for _, key := range keys {
		if _, _, hit := c.Get(key); hit {
			hits++
		}
	}

	if hits != 2 {
		t.Fatalf("expected 2 cached responses, got %d", hits)
	}
}

func TestCacheMaxBytes(t *testing.T) {
	c := New(10, 10)

	first := Key{UserID: "user-1", DocumentID: "doc-1"}
	second := Key{UserID: "user-1", DocumentID: "doc-2"}

	putResponse(c, first, Response{Body: "123456"})
	putResponse(c, second, Response{Body: "abcdef"})

	if _, _, hit := c.Get(first); hit {
		t.Fatal("expected the first response to be evicted")
	}

	got, _, hit := c.Get(second)
	if !hit || got.Body != "abcdef" {
		t.Fatal("expected the second response to be cached")
	}
}

func TestCacheOversizedResponse(t *testing.T) {
	c := New(10, 5)

	existing := Key{UserID: "user-1", DocumentID: "doc-1"}
	oversized := Key{UserID: "user-1", DocumentID: "doc-2"}

	putResponse(c, existing, Response{Body: "12345"})
	putResponse(c, oversized, Response{Body: "123456"})

	if _, _, hit := c.Get(oversized); hit {
		t.Fatal("oversized response must not be cached")
	}

	if _, _, hit := c.Get(existing); !hit {
		t.Fatal("oversized response must not evict existing responses")
	}
}

func TestCacheReplacementUpdatesSize(t *testing.T) {
	c := New(2, 10)

	first := Key{UserID: "user-1", DocumentID: "doc-1"}
	second := Key{UserID: "user-1", DocumentID: "doc-2"}

	putResponse(c, first, Response{Body: "12345678"})

	// Замена уменьшает размер первой записи с 8 до 2 байт.
	putResponse(c, first, Response{Body: "12"})
	putResponse(c, second, Response{Body: "abcdefgh"})

	for _, key := range []Key{first, second} {
		if _, _, hit := c.Get(key); !hit {
			t.Fatalf("response should fit after replacement: %+v", key)
		}
	}
}

func TestCacheInvalidationReleasesSize(t *testing.T) {
	c := New(10, 10)

	list := Key{UserID: "user-1"}
	document := Key{UserID: "user-1", DocumentID: "doc-1"}
	newDocument := Key{UserID: "user-1", DocumentID: "doc-2"}

	putResponse(c, list, Response{Body: "123456"})
	putResponse(c, document, Response{Body: "1234"})

	c.Invalidate("")

	// После удаления списка освобождаются 6 байт.
	putResponse(c, newDocument, Response{Body: "abcdef"})

	for _, key := range []Key{document, newDocument} {
		if _, _, hit := c.Get(key); !hit {
			t.Fatalf("response should fit after invalidation: %+v", key)
		}
	}
}

func TestCacheDisabled(t *testing.T) {
	tests := []struct {
		name       string
		maxEntries int
		maxBytes   int64
	}{
		{"zero entries", 0, 1024},
		{"zero bytes", 10, 0},
		{"negative entries", -1, 1024},
		{"negative bytes", 10, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(tt.maxEntries, tt.maxBytes)
			key := Key{UserID: "user-1"}

			putResponse(c, key, Response{Body: "data"})

			if _, _, hit := c.Get(key); hit {
				t.Fatal("disabled cache must not store responses")
			}
		})
	}
}

// Одновременно выполняем чтение, запись и инвалидацию.
// Проверка гонок данных выполняется при запуске с -race.
func TestCacheConcurrentAccess(t *testing.T) {
	c := New(20, 1024)

	const workers = 8
	const iterations = 100

	start := make(chan struct{})
	var wg sync.WaitGroup

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)

		go func(worker int) {
			defer wg.Done()
			<-start

			for i := 0; i < iterations; i++ {
				key := Key{
					UserID:     fmt.Sprintf("user-%d", worker),
					DocumentID: fmt.Sprintf("doc-%d", i%5),
				}

				_, generation, _ := c.Get(key)
				c.Set(key, Response{Body: "data"}, generation)
				c.Get(key)

				if i%10 == 0 {
					c.Invalidate(key.DocumentID)
				}
			}
		}(worker)
	}

	close(start)
	wg.Wait()

	// После завершения параллельных операций кеш остаётся рабочим.
	key := Key{UserID: "final-user", DocumentID: "final-doc"}
	putResponse(c, key, Response{Body: "final"})

	got, _, hit := c.Get(key)
	if !hit || got.Body != "final" {
		t.Fatal("cache must remain usable after concurrent operations")
	}
}
