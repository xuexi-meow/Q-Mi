package agent

import (
	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/tool"
	"Q-Mi/agent/workspacepath"
)

const (
	defaultGuidePath = "agent/guide.md"
	timeLayout       = "01-02 15:04"
	nowLayout        = "2006-01-02 15:04"
	maxContentRunes  = 20000
)

type approvalNeeded = tool.ApprovalRequired

func extractImageRefs(segmentsJSON []byte) []imageRef {
	return agentcontext.ExtractImageRefs(segmentsJSON)
}

func imagePart(url string) ContentPart {
	return ContentPart{Type: "image_url", ImageURL: &struct {
		URL string `json:"url"`
	}{URL: url}}
}

func napcatFilePath(workspace, path string) (string, error) {
	return workspacepath.NapcatPath(workspace, path)
}

func toolNames(tools []Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
