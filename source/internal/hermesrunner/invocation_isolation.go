package hermesrunner

import (
	"strings"

	"market-share-go/internal/hermesenv"
)

const hermesNoExternalDataToolset = "clarify"

// isolatedHermesArguments gives explanation-only model calls no terminal,
// file, browser, memory, plugin, or MCP capability. Vision is the sole
// exception and is selected only by the notice-image pipeline.
func isolatedHermesArguments(
	prompt, provider, model, usagePath, reasoning, requestedToolsets, workingDir string,
) []string {
	toolsets := hermesNoExternalDataToolset
	if strings.EqualFold(strings.TrimSpace(requestedToolsets), "vision") {
		toolsets = "vision"
	}
	return []string{
		"--safe-mode",
		"--toolsets", toolsets,
		"--in", workingDir,
		"-z", prompt,
		"--provider", provider,
		"--model", model,
		"--usage-file", usagePath,
		"--reasoning", reasoning,
	}
}

func restrictedChildEnvironmentName(name string) bool {
	return hermesenv.RestrictedDatabaseName(name)
}
