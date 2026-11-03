package notice

import "strings"

func PipelineOperation(path string) string {
	const prefix = "/internal/hermes/notices/v2/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	op := strings.TrimPrefix(path, prefix)
	switch op {
	case "capabilities", "source", "claim", "heartbeat", "object", "object_get", "page", "complete", "fail":
		return op
	default:
		return ""
	}
}

func HermesInternalRoute(path string) bool {
	return path == "/internal/hermes/notices" || PipelineOperation(path) != ""
}
