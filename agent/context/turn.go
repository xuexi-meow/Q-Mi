package context

import (
	"encoding/json"
	"sync"
	"time"
)

const (
	WakeChat       = "chat"
	WakeBackground = "background"
)

type Turn struct {
	ChatType    string
	ChatID      int64
	Wake        string
	SelfID      int64
	Now         time.Time
	Mood        string
	Notes       []Note
	NewMessages []TurnMessage

	pendingMu     sync.Mutex
	pendingImages []string
}

func (t *Turn) AddPendingImage(dataURL string) {
	if t == nil {
		return
	}
	t.pendingMu.Lock()
	t.pendingImages = append(t.pendingImages, dataURL)
	t.pendingMu.Unlock()
}

func (t *Turn) PendingImages() []string {
	if t == nil {
		return nil
	}
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	return append([]string(nil), t.pendingImages...)
}

func (t *Turn) ClearPendingImages() {
	if t == nil {
		return
	}
	t.pendingMu.Lock()
	t.pendingImages = nil
	t.pendingMu.Unlock()
}

type TurnMessage struct {
	MessageID  int64
	Wake       string
	ChatType   string
	ChatID     int64
	SenderID   int64
	SenderName string
	Text       string
	Segments   json.RawMessage
	Images     []ImageRef
	IsSelf     bool
	Mentioned  bool
	Urgent     bool
	Time       time.Time
}

type ImageRef struct {
	Idx int
	URL string
}

type Note struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
}
