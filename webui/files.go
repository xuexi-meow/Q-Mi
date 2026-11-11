package webui

import "fmt"

func promptFilePath(name string) (string, error) {
	fallback, ok := fileWhitelist[name]
	if !ok {
		return "", fmt.Errorf("name 仅支持 persona 或 guide")
	}
	if store := configStoreProvider(); store != nil {
		cfg := store.Snapshot()
		if cfg != nil && cfg.Agent != nil {
			if name == "persona" {
				return cfg.Agent.PersonaFile, nil
			}
			return cfg.Agent.GuideFile, nil
		}
	}
	return fallback, nil
}
