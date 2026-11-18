package agent

import (
	"os"
	"path/filepath"

	"Q-Mi/agent/tools/sticker"
	"Q-Mi/storage"
)

func mfaceCDNURL(emojiID string) string {
	return sticker.MFaceCDNURL(emojiID)
}

func stickerPreviewCandidates(kind, url, emojiID string) []string {
	return sticker.PreviewCandidates(kind, url, emojiID)
}

func ensureStickerPreview(ctx *ToolContext, record *storage.Sticker) string {
	return sticker.EnsurePreview(stickerDependencies(ctx), record)
}

func stickerPreviewPath(images *imageStore, hash string) string {
	if images == nil || hash == "" {
		return ""
	}
	path := filepath.Join(images.dir, hash)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}
