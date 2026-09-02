package publish

import (
	"encoding/json"

	"ares/internal/entity"
	"ares/internal/tool"
)

var nullablePipelineParameterKeys = []string{
	"domain",
	"code_package_path",
	"code_package_name",
	"base_image",
	"pre_stop_command",
}

func normalizeLegacyPipelineParameters(parameters map[string]string) {
	for _, key := range nullablePipelineParameterKeys {
		if value, exists := parameters[key]; exists {
			parameters[key] = tool.NormalizeNullableText(value)
		}
	}
}

func normalizePipelineParamJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var parameters map[string]string
	if err := json.Unmarshal(raw, &parameters); err != nil {
		return raw
	}
	normalizeLegacyPipelineParameters(parameters)
	normalized, err := json.Marshal(parameters)
	if err != nil {
		return raw
	}
	return normalized
}

func normalizeTaskRecordNullableText(record *entity.TaskRecord) {
	if record == nil {
		return
	}
	record.Message = tool.NormalizeNullableText(record.Message)
	record.RundeckAppName = normalizeNullableTextPointer(record.RundeckAppName)
	record.CiJobName = tool.NormalizeNullableText(record.CiJobName)
	record.CdJobName = tool.NormalizeNullableText(record.CdJobName)
	record.Products = tool.NormalizeNullableText(record.Products)
	record.PipelineParam = normalizePipelineParamJSON(record.PipelineParam)
}

func normalizeNullableTextPointer(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := tool.NormalizeNullableText(*value)
	if normalized == "" {
		return nil
	}
	return &normalized
}
