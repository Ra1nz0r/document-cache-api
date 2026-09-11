package cache

import "sync"

// Key определяет кешируемый запрос.
// Пустой DocumentID означает список документов.
type Key struct {
	UserID     string // ID пользователя
	DocumentID string // ID документа
	Query      string // Поисковый запрос
}

// Response содержит готовый ответ из кеша.
type Response struct {
	ContentType string // MIME-тип ответа
	Body        string // Тело ответа
	NoSniff     bool   // true, если нужно добавить заголовок X-Content-Type-Options: nosniff
}

type Cache struct {
	mu sync.RWMutex // Защищает items, bytes и generation.

	items      map[Key]Response // Кешированные ответы.
	bytes      int64            // Текущий суммарный размер закешированных тел ответов.
	maxEntries int              // Максимальное количество записей.
	maxBytes   int64            // Максимальный суммарный размер закешированных тел ответов.
	generation uint64           // Номер текущей версии кеша. Увеличивается при Invalidate.
}

// New создаёт новый кеш с указанными лимитами.
func New(maxEntries int, maxBytes int64) *Cache {
	return &Cache{
		items:      make(map[Key]Response),
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
	}
}

// Get возвращает ответ и текущую версию кеша.
func (c *Cache) Get(key Key) (Response, uint64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Возвращаем копию, чтобы внешний код не мог изменить кеш.
	response, ok := c.items[key]

	return response, c.generation, ok
}

// Set сохраняет ответ, если с момента Get кеш не инвалидировался.
func (c *Cache) Set(key Key, response Response, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Не даём старому запросу записать результат после инвалидации.
	if generation != c.generation {
		return
	}

	size := int64(len(response.Body))

	// Не сохраняем слишком большие ответы или при нулевых лимитах.
	if c.maxEntries <= 0 || c.maxBytes <= 0 || size > c.maxBytes {
		return
	}

	// При замене записи сначала освобождаем занятый ей размер.
	if previous, ok := c.items[key]; ok {
		c.bytes -= int64(len(previous.Body))
		delete(c.items, key)
	}

	// Удаляем старые записи, пока новая не помещается в лимиты.
	for len(c.items) >= c.maxEntries || size > c.maxBytes-c.bytes {
		for oldKey, oldResponse := range c.items {
			c.bytes -= int64(len(oldResponse.Body))
			delete(c.items, oldKey)
			break
		}
	}

	c.items[key] = response
	c.bytes += size
}

// Invalidate сбрасывает кеш списков и указанного документа.
// Пустой documentID означает только списки.
func (c *Cache) Invalidate(documentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Не даём старому запросу записать результат после инвалидации.
	c.generation++

	for key, response := range c.items {
		if key.DocumentID == "" ||
			(documentID != "" && key.DocumentID == documentID) {
			c.bytes -= int64(len(response.Body))
			delete(c.items, key)
		}
	}
}
