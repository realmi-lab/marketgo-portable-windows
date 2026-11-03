package app

// ValidateNoticeDraftScope reuses the public notice access policy without starting an app service.
func ValidateNoticeDraftScope(text string) error { return validatePublicLLMQuestionScope(text, nil) }
