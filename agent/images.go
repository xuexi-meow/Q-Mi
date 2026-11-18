package agent

import (
	"net/http"
	"path/filepath"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/storage"
)

// imageStore preserves the agent package's internal image API while the
// implementation is owned by agent/context.
type imageStore struct {
	inner *agentcontext.ImageStore
	dir   string
}

func newImageStore(dir string, store *storage.Store) *imageStore {
	inner := agentcontext.NewImageStore(dir, store)
	return &imageStore{inner: inner, dir: inner.Directory()}
}

func newImageStoreWithClient(dir string, store *storage.Store, client *http.Client) *imageStore {
	inner := agentcontext.NewImageStoreWithClient(dir, store, client)
	return &imageStore{inner: inner, dir: inner.Directory()}
}

func (s *imageStore) dataURLFor(messageID int64, idx int, url string) string {
	if s == nil {
		return url
	}
	return s.inner.DataURLFor(messageID, idx, url)
}

func (s *imageStore) download(url string) ([]byte, string, error) {
	return s.inner.Download(url)
}

func (s *imageStore) save(messageID int64, idx int, data []byte, mime string) string {
	return s.inner.Save(messageID, idx, data, mime)
}

func (s *imageStore) dataURLFromRef(ref storage.ImageRef) string {
	return s.inner.DataURLFromRef(ref)
}

func (s *imageStore) pathForHash(hash string) string {
	return filepath.Join(s.dir, filepath.Base(hash))
}
