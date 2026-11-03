package hermesrunner

import "context"

// Shared process adapter; feature prompts and state belong to the caller.
const InteractiveNoticeModel = noticeQuestionPrimaryModel
const InteractiveNoticeEffort = noticeQuestionPrimaryEffort

func InvokeInteractiveNotice(ctx context.Context, config MarketQuestionConfig, prompt, model, effort, dir string) ([]byte, error) {
	release, err := beginInteractiveQuestionPriority(config.OutputDir)
	if err != nil {
		return nil, err
	}
	defer release()
	raw, err := invokeHermesMarketQuestion(prepareQuestionCall(ctx, "notice_llm2"), config, prompt, model, effort, dir)
	if err != nil {
		return nil, err
	}
	normalized, _ := normalizeHermesUTF8(raw)
	return normalized, nil
}
